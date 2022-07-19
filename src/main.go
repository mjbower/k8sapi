package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var cs *kubernetes.Clientset

var localMode bool

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Namespace struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

type Pod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
	Action    string `json:"action"`
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

/*
Create a connection to the cluster, use -l flag for local connection
*/
func connectKubeAPI() {
	log.Print("Connecting to the Kubernetes API ")
	if localMode {
		log.Print("- Running in local mode")
	}

	var config *rest.Config
	var err error

	var kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")

	if localMode {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else { // relies on injection of /var/run/secrets/kubernetes.io/serviceaccount
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		panic(err)
	}

	// create the client set
	cs, err = kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
}

/*
Returns a json list of Namespaces in the cluster
*/
func getNamespaces(w http.ResponseWriter, r *http.Request) {
	nameSpaces, err := cs.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	fmt.Printf("There are %d Namespaces in the cluster\n", len(nameSpaces.Items))
	var nsList []Namespace
	for _, ns := range nameSpaces.Items {
		nsItem := Namespace{
			Name:   ns.Name,
			Action: "add",
		}
		nsList = append(nsList, nsItem)
	}
	jsonResp, err := json.Marshal(nsList)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResp)
}

func send_WS(conn *websocket.Conn, json []byte) {
	messageType := 1
	if err := conn.WriteMessage(messageType, json); err != nil {
		log.Println(err)
		return
	}
}

func reader(conn *websocket.Conn) {
	log.Println("Opened Websocket to send pod data")

	// stop signal for the informer
	stopper := make(chan struct{})
	defer close(stopper)

	factory := informers.NewSharedInformerFactory(cs, 0)
	podInformer := factory.Core().V1().Pods()
	informer := podInformer.Informer()

	defer runtime.HandleCrash()

	// start informer ->
	go factory.Start(stopper)

	// start to sync and call list
	if !cache.WaitForCacheSync(stopper, informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { // register add Handler
			mObj, ok := obj.(*corev1.Pod)
			if !ok {
				log.Panic("Not a Pod added")
			}
			pStatus := getPodStatus(mObj)

			json := createJson("add", mObj.Name, mObj.Namespace, pStatus)
			send_WS(conn, json)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) { // register update Handler
			oObj, ok := oldObj.(*corev1.Pod)
			nObj, ok := newObj.(*corev1.Pod)
			if !ok {
				log.Panic("Not a Pod added")
			}

			// if we get a pod Running, but with 0/1 , we don't detect that.

			var pStatus string
			pStatus = getPodStatus(oObj)
			//fmt.Printf("ZZ OLD Status Pod(%s) namespace(%s) Status(%s)\n",oObj.Name,oObj.Namespace,pStatus)
			pStatus = getPodStatus(nObj)
			json := createJson("add", nObj.Name, nObj.Namespace, pStatus)
			send_WS(conn, json)
		},
		DeleteFunc: func(obj interface{}) { // register delete Handler
			mObj, ok := obj.(*corev1.Pod)
			if !ok {
				log.Panic("Not a Pod added")
			}

			json := createJson("delete", mObj.Name, mObj.Namespace, "deleted")
			send_WS(conn, json)
		},
	})

	<-stopper

	// for {
	// 	// get pods in all the namespaces by omitting namespace
	// 	// Or specify namespace to get pods in particular namespace
	// 	pods, err := cs.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	// 	if err != nil {
	// 		panic(err.Error())
	// 	}
	// 	fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))

	// 	var podList []Pod
	// 	for _, pod := range pods.Items {
	// 		podItem := Pod{
	// 			Name:      pod.GetName(),
	// 			Namespace: pod.Namespace,
	// 			Status:    string(pod.Status.Phase),
	// 			Action:    "haha,,fix this",
	// 		}
	// 		fmt.Println("XXX", podItem)
	// 		podList = append(podList, podItem)
	// 	}

	// 	jsonResp, err := json.Marshal(podList)
	// 	if err != nil {
	// 		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	// 	}
	// 	fmt.Println("YYY", string(jsonResp))
	// 	// 	messageType, p, err := conn.ReadMessage()
	// 	// 	if err != nil {
	// 	// 		log.Println(err)
	// 	// 		return
	// 	// 	}
	// 	// 	log.Println("Received MSG :", string(p))
	// 	messageType := 1
	// 	if err := conn.WriteMessage(messageType, jsonResp); err != nil {
	// 		log.Println(err)
	// 		return
	// 	}

	// 	time.Sleep(5 * time.Second)
	// }
}

func onAdd(obj interface{}) {
	mObj, ok := obj.(*corev1.Pod)
	if !ok {
		log.Panic("Not a Pod added")
	}
	pStatus := getPodStatus(mObj)

	json := createJson("add", mObj.Name, mObj.Namespace, pStatus)
	_ = json // SEND WEBSOCKET
}

func onUpdate(oldObj interface{}, newObj interface{}) {
	oObj, ok := oldObj.(*corev1.Pod)
	nObj, ok := newObj.(*corev1.Pod)
	if !ok {
		log.Panic("Not a Pod added")
	}

	// if we get a pod Running, but with 0/1 , we don't detect that.

	// fmt.Printf("Old Pod Updated Name(%s) Phase(%s) Status(%v)\n",oObj.Name,oObj.Status.Phase,oObj.Status.ContainerStatuses)
	// fmt.Printf("New Pod Updated Name(%s) Phase(%s) \n",nObj.Name,nObj.Status.Phase)
	var pStatus string
	pStatus = getPodStatus(oObj)
	//fmt.Printf("ZZ OLD Status Pod(%s) namespace(%s) Status(%s)\n",oObj.Name,oObj.Namespace,pStatus)
	pStatus = getPodStatus(nObj)
	json := createJson("add", nObj.Name, nObj.Namespace, pStatus)
	_ = json // SEND WEBSOCKET
	//fmt.Printf("ZZ NEW Status Pod(%s) namespace(%s) Status(%s)\n",nObj.Name,nObj.Namespace,pStatus)
}

func onDelete(obj interface{}) {
	mObj, ok := obj.(*corev1.Pod)
	if !ok {
		log.Panic("Not a Pod added")
	}

	json := createJson("delete", mObj.Name, mObj.Namespace, "deleted")
	_ = json // SEND WEBSOCKET
}

func createJson(action, name, ns, status string) []byte {
	newPod := Pod{
		Action:    action,
		Name:      name,
		Namespace: ns,
		Status:    status,
	}
	jsonMsg, err := json.Marshal(newPod)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}
	fmt.Printf("\tSent %s\n", jsonMsg)
	return jsonMsg
}

/*
Returns a json object listing Pods,  specify "ns" to limit the list to a Namespace
*/
func wsGetPods(w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error happened.  Err: %s/n", err)
	}

	fmt.Printf("Websocket opened to send pod data\n")
	reader(ws)
}

/*
Returns a json object listing Pods,  specify "ns" to limit the list to a Namespace
*/
func getPods(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ns := vars["ns"]

	pods, err := cs.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	fmt.Printf("There are %d pods in NS(%s)\n", len(pods.Items), ns)

	var podList []Pod
	for _, pod := range pods.Items {
		podItem := Pod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    getPodStatus(&pod),
			Action:    "add",
		}
		podList = append(podList, podItem)
	}
	jsonResp, err := json.Marshal(podList)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResp)
}

func getPodStatus(pod *corev1.Pod) string {
	restarts := 0
	//totalContainers := len(pod.Spec.Containers)
	readyContainers := 0

	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}
	//fmt.Printf("XX %s - Pod (start) Name(%s) REASON %s\n",someText, pod.Name,reason)

	// switch pod.Status.Phase {
	// case corev1.PodSucceeded:
	// 	fmt.Printf("XXXX Pod Succeeded %s\n",corev1.podSuccessConditions)
	// case corev1.PodFailed:
	// 	fmt.Printf("XXXX Pod Failed %s\n",corev1.podFailedConditions)
	// }

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += int(container.RestartCount)
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("XX Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("XX Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "XX Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "XX Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("XX Init:%d/%d\n", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}
	if !initializing {
		restarts = 0
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += int(container.RestartCount)
			if container.State.Waiting != nil && container.State.Waiting.Reason != "" {
				reason = container.State.Waiting.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason != "" {
				reason = container.State.Terminated.Reason
			} else if container.State.Terminated != nil && container.State.Terminated.Reason == "" {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("XX Signal:%d\n", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("XX ExitCode:%d\n", container.State.Terminated.ExitCode)
				}
			} else if container.Ready && container.State.Running != nil {
				hasRunning = true
				readyContainers++
			}
		}

		// change pod status back to "Running" if there is at least one container still reporting as "Running" status
		if reason == "Completed" && hasRunning {
			if hasPodReadyCondition(pod.Status.Conditions) {
				reason = "Running"
			} else {
				reason = "NotReady"
			}
		}
	}

	if pod.DeletionTimestamp != nil {
		reason = "Terminating"
	}
	// SEND ME to WEBSOCKET
	//fmt.Printf("XX %s - Final (finish) Name(%s) REASON %s\n",someText,pod.Name,reason)
	return reason
}

func hasPodReadyCondition(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

/*
Delete a Pod in a specific Namespace,  "ns" and "pname" are required
*/
func deletePod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ns := vars["ns"]
	pname := vars["pname"]
	// No need to check the Vars, gorillamux and StrictSlash does that for us

	fmt.Printf("Deleting Pod (%s) in NS(%s)", pname, ns)
	err := cs.CoreV1().Pods(ns).Delete(context.TODO(), pname, metav1.DeleteOptions{})
	if err != nil {
		fmt.Printf("Pod deletion Failed  Pod(%s) NS(%s) Error(%s)\n", pname, ns, err)
	}
}

/*
Define the routes and variables for our paths
*/
func handleRequests() {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/namespaces", getNamespaces)
	router.HandleFunc("/pods", getPods)
	router.HandleFunc("/pods/{ns}", getPods)
	router.HandleFunc("/deletePod/{ns}/{pname}", deletePod)
	router.HandleFunc("/ws/pods", wsGetPods)

	// Access-Control-Allow-Origin: *

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})

	handler := c.Handler(router)
	log.Fatal(http.ListenAndServe(":8080", handler))

}

func main() {
	// Make a connection to a cluster and provide an API to list Pods/Namespaces,  and delete Pods

	flag.BoolVar(&localMode, "l", false, "Turn on local running mode")
	flag.Parse()
	log.SetOutput(os.Stdout)

	connectKubeAPI()

	fmt.Println("******* Program start: *********")

	handleRequests()
}
