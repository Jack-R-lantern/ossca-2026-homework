package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/unshare/netns", UserHandler)

	fmt.Println("Server starting on :8080...")
	http.ListenAndServe(":8080", nil)
}
