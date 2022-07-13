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
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var cs *kubernetes.Clientset

var localMode bool

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
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

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")

	jsonResp, err := json.Marshal(nameSpaces)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}
	w.Write(jsonResp)
}

func reader(conn *websocket.Conn) {
	wsTimer := 10
	websocketloop, ok := os.LookupEnv("WEBSOCKETLOOP")

	if ok {
		var err error
		wsTimer, err = strconv.Atoi(websocketloop)

		if err != nil {
			// ... handle error
			panic(err)
		}
	}
	fmt.Println("Websocket loop time set to ", wsTimer)

	log.Println("Opened Websocket to send pod data")
	for {
		// get pods in all the namespaces by omitting namespace
		// Or specify namespace to get pods in particular namespace
		pods, err := cs.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))

		type Pod struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Status    string `json:"status"`
		}
		var podList []Pod
		for _, pod := range pods.Items {
			podItem := Pod{
				Name:      pod.GetName(),
				Namespace: pod.Namespace,
				Status:    string(pod.Status.Phase),
			}
			fmt.Println("XXX", podItem)
			podList = append(podList, podItem)
		}

		jsonResp, err := json.Marshal(podList)
		if err != nil {
			log.Fatalf("Error happened in JSON marshal. Err: %s", err)
		}
		fmt.Println("YYY", string(jsonResp))
		// 	messageType, p, err := conn.ReadMessage()
		// 	if err != nil {
		// 		log.Println(err)
		// 		return
		// 	}
		// 	log.Println("Received MSG :", string(p))
		messageType := 1
		if err := conn.WriteMessage(messageType, jsonResp); err != nil {
			log.Println(err)
			return
		}

		time.Sleep(5 * time.Second)
	}
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

	fmt.Fprintf(w, "websocket opened for namespace %v\n", ws)
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

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")

	jsonResp, err := json.Marshal(pods)
	if err != nil {
		log.Fatalf("Error happened in JSON marshal. Err: %s", err)
	}
	w.Write(jsonResp)
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
