package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	ascii "github.com/CrowdSurge/banner"
	"github.com/gorilla/websocket"
	svc "github.com/mch1307/whoamI-consul/svc"
)

//const service = "whoamI"

var (
	port, consulPort                                                int
	hostip, consulAddr, consulToken, kvPath, consulPortStr, service string
	hostname, _                                                     = os.Hostname()
)

func init() {
	flag.IntVar(&port, "port", 8080, "Port number for HTTP listen")
	flag.StringVar(&consulAddr, "consul", "", "Consul service catalog address")
	flag.IntVar(&consulPort, "consulPort", 8500, "Consul service catalog port")
	flag.StringVar(&consulToken, "consulToken", "", "Consul ACL token (optional)")
	flag.StringVar(&kvPath, "kvPath", "PUBLIC/whoamI", "Consul KV path for banner (optional)")
	flag.StringVar(&service, "service", "whoamI", "Service name that will be registered (fqdn better)")
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {
	// defer profile.Start().Stop()
	flag.Parse()
	consulPortStr = strconv.Itoa(consulPort)
	http.HandleFunc("/echo", echoHandler)
	http.HandleFunc("/bench", benchHandler)
	http.HandleFunc("/", whoamI)
	http.HandleFunc("/api", api)
	http.HandleFunc("/health", healthHandler)
	// create new Consul client instance
	consulCli, err := svc.NewClient(consulAddr, consulPortStr, consulToken)

	// Register the service to Consul catalog
	err = svc.RegisterService(consulCli.Agent(), service, hostname, "http", port)
	if err != nil {
		fmt.Printf("Encountered error registering a service with consul -> %s\n", err)
	}

	go http.ListenAndServe(":"+strconv.Itoa(port), nil)
	fmt.Println("Starting up on port " + strconv.Itoa(port))

	// create channel for post exit cleanup
	signalChan := make(chan os.Signal, 1)
	cleanupDone := make(chan bool)
	signal.Notify(signalChan, os.Interrupt)
	signal.Notify(signalChan, os.Kill)
	go func() {
		for _ = range signalChan {
			fmt.Println("Received interrupt, deregistering service...")
			svc.DeregisterService(consulCli.Agent(), service)
			cleanupDone <- true
		}
	}()
	<-cleanupDone
}

func printBinary(s []byte) {
	fmt.Printf("Received b:")
	for n := 0; n < len(s); n++ {
		fmt.Printf("%d,", s[n])
	}
	fmt.Printf("\n")
}
func benchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Content-Type", "text/plain")

}
func echoHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			return
		}
		printBinary(p)
		err = conn.WriteMessage(messageType, p)
		if err != nil {
			return
		}
	}
}

func whoamI(w http.ResponseWriter, req *http.Request) {
	u, _ := url.Parse(req.URL.String())
	queryParams := u.Query()
	wait := queryParams.Get("wait")
	if len(wait) > 0 {
		duration, err := time.ParseDuration(wait)
		if err == nil {
			time.Sleep(duration)
		}
	}
	// Get banner from Consul KV
	consulCli, _ := svc.NewClient(consulAddr, consulPortStr, consulToken)

	banner, err := svc.GetKV(consulCli, kvPath+"/banner")
	if err != nil {
		fmt.Println("Error getting banner from k/v", err)
		banner = "not_found"
	}
	if banner == "not_found" {
		banner = "whoamI"
	}
	fmt.Fprintln(w, ascii.PrintS(strings.ToLower(banner)))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Request served by host:", hostname)
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		// handle err
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			fmt.Fprintln(w, "IP:", ip)
		}
	}
	//req.Write(w)
}

func api(w http.ResponseWriter, req *http.Request) {
	hostname, _ := os.Hostname()
	data := struct {
		Hostname string      `json:"hostname,omitempty"`
		IP       []string    `json:"ip,omitempty"`
		Headers  http.Header `json:"headers,omitempty"`
	}{
		hostname,
		[]string{},
		req.Header,
	}

	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		// handle err
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			data.IP = append(data.IP, ip.String())
		}
	}
	json.NewEncoder(w).Encode(data)
}

type healthState struct {
	StatusCode int
}

var currentHealthState = healthState{200}
var mutexHealthState = &sync.RWMutex{}

func healthHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		var statusCode int
		err := json.NewDecoder(req.Body).Decode(&statusCode)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(err.Error()))
		} else {
			fmt.Printf("Update health check status code [%d]\n", statusCode)
			mutexHealthState.Lock()
			defer mutexHealthState.Unlock()
			currentHealthState.StatusCode = statusCode
		}
	} else {
		mutexHealthState.RLock()
		defer mutexHealthState.RUnlock()
		w.WriteHeader(currentHealthState.StatusCode)
	}
}
