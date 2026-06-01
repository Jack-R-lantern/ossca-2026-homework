package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("POST /netns", UserNSHandler)
	http.HandleFunc("POST /netns/{name}/veth", VethHandler)
	http.HandleFunc("POST /netns/{name}/exec", ExecHandler)

	fmt.Println("Server starting on :8080...")
	http.ListenAndServe(":8080", nil)
}
