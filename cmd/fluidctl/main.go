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
	appID := flag.String("app", "", "application ID")
	ip := flag.String("ip", "", "application IP")
	flag.Parse()

	if *appID == "" || *ip == "" {
		fmt.Fprintln(os.Stderr, "usage: fluidctl -server http://127.0.0.1:18080 -app <id> -ip <ip>")
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
}
