package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	server := flag.String("server", "http://127.0.0.1:18080", "agent base URL")
	mode := flag.String("mode", "create-app", "mode: create-app or raft-join")
	appID := flag.String("app", "", "application ID")
	ip := flag.String("ip", "", "application IP")
	raftID := flag.String("raft-id", "", "raft server ID (for raft-join)")
	raftAddr := flag.String("raft-addr", "", "raft address host:port (for raft-join)")
	flag.Parse()

	switch *mode {
	case "create-app":
		if *appID == "" || *ip == "" {
			fmt.Fprintln(os.Stderr, "usage: fluidctl -mode create-app -server URL -app <id> -ip <ip>")
			os.Exit(2)
		}
		payload, _ := json.Marshal(map[string]string{"appId": *appID, "ip": *ip})
		resp, err := http.Post(*server+"/v1/apps", "application/json", bytes.NewReader(payload))
		if err != nil {
			fmt.Fprintln(os.Stderr, "request failed:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Println("status:", resp.Status)
		io.Copy(os.Stdout, resp.Body)
	case "raft-join":
		if *raftID == "" || *raftAddr == "" {
			fmt.Fprintln(os.Stderr, "usage: fluidctl -mode raft-join -server URL -raft-id <id> -raft-addr <host:port>")
			os.Exit(2)
		}
		payload, _ := json.Marshal(map[string]string{"id": *raftID, "addr": *raftAddr})
		resp, err := http.Post(*server+"/raft/join", "application/json", bytes.NewReader(payload))
		if err != nil {
			fmt.Fprintln(os.Stderr, "request failed:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Println("status:", resp.Status)
		io.Copy(os.Stdout, resp.Body)
	default:
		fmt.Fprintln(os.Stderr, "unknown mode; use create-app or raft-join")
		os.Exit(2)
	}
}
