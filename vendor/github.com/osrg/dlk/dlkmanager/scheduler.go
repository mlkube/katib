package main

import (
	"fmt"
	"time"

	"github.com/osrg/dlk/dlkmanager/configs"
	"github.com/osrg/dlk/dlkmanager/datastore"

	"sync"

	api "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type node struct {
	node            *api.Node
	nrGPUs          int
	nrAvailableGPUs int
}

type k8scheduler struct {
	nodes         []string
	nodeTok8sNode map[string]*node
	nodesItr      int
	nodeMux       sync.Mutex
	client        *client
}

var (
	ltCompCh chan *learningTask
)

func init() {
	ltCompCh = make(chan *learningTask)
}

func schedulerNew(client *client, maxTasksPerPu int) *k8scheduler {
	return &k8scheduler{
		nodeTok8sNode: make(map[string]*node),
		client:        client,
	}
}

func nodeConv(n *api.Node) *node {
	nrGPUs := int64(-1)
	if v, ok := n.Status.Allocatable["alpha.kubernetes.io/nvidia-gpu"]; ok {
		nrGPUs, _ = v.AsInt64()
	}

	newNode := &node{
		node:            n,
		nrGPUs:          int(nrGPUs),
		nrAvailableGPUs: int(nrGPUs),
	}

	return newNode
}

func schedMain() {
	// Initialize the kubernetes client
	config := clientConfig{Addr: configs.Pflg.Addr, SchedulerName: configs.Pflg.Scheduler}
	client, err := clientNew(config, configs.Pflg.Pcs)
	if err != nil {
		fmt.Printf("kube client error: %v\n", err)
		panic(err)
	}
	fmt.Printf("kube client configured: \n")

	// Initialize the scheduler
	scheduler := schedulerNew(client, configs.Pflg.Mt)

	nodes, err := client.GetClientset().CoreV1().Nodes().List(meta_v1.ListOptions{})
	if err != nil {
		panic(err)
	}
	scheduler.nodes = make([]string, 0)
	for _, node := range nodes.Items {
		for _, status := range node.Status.Conditions {
			if status.Type == "Ready" && string(status.Status) == "True" {
				fmt.Printf("adding a new node: %s\n", node.Name)
				scheduler.nodes = append(scheduler.nodes, node.Name)
				break
			}
		}
	}

	// Start the scheduler
	scheduler.run(client)
}

// The main workflow of the scheduler happens here
func (ks *k8scheduler) run(client *client) {
	nAddCh := ks.client.GetNodeAddChan()
	nUpdateCh := ks.client.GetNodeUpdateChan()
	nDeleteCh := ks.client.GetNodeDeleteChan()

	podCh := make(chan *api.Pod)
	ltCh := make(chan *learningTask)

	go func() {
		for {
			newPods := client.GetPodBatch(time.Duration(configs.Pflg.Pbt) * time.Second)
			for _, pod := range newPods {
				podCh <- pod
			}
		}
	}()

	pendingLts := make([]*learningTask, 0)

	// Loop: Read pods, Schedule, and Assign Bindings
	for {
		select {
		case pod := <-podCh:

			runningLTMu.Lock()
			var lt *learningTask
			var ok bool
			if lt, ok = runningLearningTasks[pod.Labels["learning-task"]]; !ok {
				fmt.Printf("unknown learning task: %s\n", pod.Labels["learning-task"])
				runningLTMu.Unlock()
				continue
			}
			runningLTMu.Unlock()

			if lt.running {
				// the pod is evicted so the entire learning task must be restarted
				fmt.Printf("learning task %s is restarted for handling eviction of pod %s\n", lt.name, pod.Name)
				go func(lt *learningTask) {
					notifyCh := make(chan struct{})
					lt.stopCh <- notifyCh
					<-notifyCh

					lt.pods = make([]*api.Pod, 0)
					lt.nrReadyPSes = 0
					lt.nrReadyWorkers = 0
					lt.running = false

					lt.run()
				}(lt)

				continue
			}

			fmt.Printf("learningTaskMaker: handling newly arrived pod %s\n", pod.Name)
			lt.pods = append(lt.pods, pod)

			if pod.Labels["type"] == "PS" {
				lt.nrReadyPSes++
			} else if pod.Labels["type"] == "worker" {
				lt.nrReadyWorkers++
			}

			if lt.nrPSes == lt.nrReadyPSes && lt.nrWorkers == lt.nrReadyWorkers {
				// ready to be scheduled
				fmt.Printf("learning task %s is ready to be scheduled\n", lt.name)
				go func(lt *learningTask) {
					ltCh <- lt
				}(lt)
			}

		case lt := <-ltCh:
			fmt.Printf("Adding Pods of learning task %s as tasks to scheduler\n", lt.name)

			podToNodeBindings := make([]*api.Binding, 0)

			usingGPUsPerPod := make(map[*api.Pod]int)
			podToNode := make(map[*api.Pod]*api.Node)

			skipThisLt := false

			for _, pod := range lt.pods {
				var totalRequireGPU int64 = 0
				for _, c := range pod.Spec.Containers {
					if v, ok := c.Resources.Requests["alpha.kubernetes.io/nvidia-gpu"]; ok {
						requireGPU, _ := v.AsInt64()
						totalRequireGPU += requireGPU
					}
				}
				if totalRequireGPU > 0 {
					entryItr := ks.nodesItr
					enable := true
					for {
						candidateNode := ks.nodeTok8sNode[ks.nodes[entryItr]]
						if candidateNode.nrGPUs != -1 {
							allocatableGPU := candidateNode.nrAvailableGPUs
							fmt.Printf("allocatable GPU of %s: %d\n", candidateNode.node.Name, allocatableGPU)
							if int(totalRequireGPU) <= allocatableGPU {
								ks.nodesItr = entryItr
								break
							}
						}

						entryItr++
						entryItr %= len(ks.nodes)
						if entryItr == ks.nodesItr {
							enable = false
							break
						}
					}
					if !enable {
						skipThisLt = true
						break
					}
				}

				usingGPUsPerPod[pod] = int(totalRequireGPU)
				podToNode[pod] = ks.nodeTok8sNode[ks.nodes[ks.nodesItr]].node

				podToNodeBindings = append(podToNodeBindings,
					&api.Binding{
						ObjectMeta: meta_v1.ObjectMeta{Namespace: configs.Pflg.Ns, Name: pod.Name, UID: pod.UID},
						Target: api.ObjectReference{
							Kind: "Node",
							Name: ks.nodes[ks.nodesItr]},
					},
				)
				fmt.Printf("pod %s -> node %s\n", pod.Name, ks.nodes[ks.nodesItr])

				if usingGPUsPerPod[pod] > 0 {
					nodenm := ks.nodes[ks.nodesItr]
					ks.nodeTok8sNode[nodenm].nrAvailableGPUs -= usingGPUsPerPod[pod]
					fmt.Printf("node %s's current available GPUs: %d\n", nodenm, ks.nodeTok8sNode[nodenm].nrAvailableGPUs)
					if ks.nodeTok8sNode[nodenm].nrAvailableGPUs < 0 {
						panic(fmt.Sprintf("node %s's available GPU is negative: %d", nodenm, ks.nodeTok8sNode[nodenm].nrAvailableGPUs))
					}
				}
				ks.nodesItr++
				ks.nodesItr %= len(ks.nodes)
			}

			if skipThisLt {
				log.Printf("pending learning task %s because of the GPU shortage", lt.name)
				pendingLts = append(pendingLts, lt)
				continue
			}

			ks.client.AssignBinding(configs.Pflg.Ns, podToNodeBindings)

			lt.usingGPUsPerPod = usingGPUsPerPod
			lt.podToNode = podToNode

			datastore.Accesor.UpdateState(lt.name, ltStateNotCompleted, "") // for the case of stopped -> not completed
			lt.running = true

		case lt := <-ltCompCh:
			// IMPORTANT TODO: nrAvailableGPUs should be updated after completion of individual workers for increasing utilization
			for pod, node := range lt.podToNode {
				ks.nodeTok8sNode[node.Name].nrAvailableGPUs += lt.usingGPUsPerPod[pod]
			}

			go func(pending []*learningTask, clt *learningTask) {
				for _, plt := range pending {
					// Completed learning task is removed from pending ones
					if clt == plt {
						continue
					}
					ltCh <- plt
				}
			}(pendingLts, lt)

			pendingLts = make([]*learningTask, 0)

		case node := <-nAddCh:
			if _, ok := ks.nodeTok8sNode[node.Name]; ok {
				continue
			}
			ks.nodeTok8sNode[node.Name] = nodeConv(node)
			fmt.Printf("Node %s is added. Available GPUs: %d.\n", node.Name, ks.nodeTok8sNode[node.Name].nrAvailableGPUs)

			found := false
			for _, n := range ks.nodes {
				if n == node.Name {
					found = true
					break
				}
			}

			if !found {
				ks.nodes = append(ks.nodes, node.Name)
			}

		case node := <-nUpdateCh:
			origAvailGPUs := -1

			if n_, ok := ks.nodeTok8sNode[node.Name]; !ok {
				// probably we missed an add notification
				found := false
				for _, n := range ks.nodes {
					if n == node.Name {
						found = true
						break
					}
				}

				if !found {
					// we really missed the add notification
					ks.nodes = append(ks.nodes, node.Name)
				}
			} else {
				origAvailGPUs = n_.nrAvailableGPUs
			}

			ks.nodeTok8sNode[node.Name] = nodeConv(node)
			if origAvailGPUs != -1 {
				ks.nodeTok8sNode[node.Name].nrAvailableGPUs = origAvailGPUs
			}

		case node := <-nDeleteCh:
			if _, ok := ks.nodeTok8sNode[node.Name]; ok {
				delete(ks.nodeTok8sNode, node.Name)
			}

			for i := range ks.nodes {
				if ks.nodes[i] == node.Name {
					ks.nodes = append(ks.nodes[:i], ks.nodes[i+1:]...)
					break
				}
			}
			ks.nodesItr = 0

			// do not need to handle pod evict here
		}
	}
}
