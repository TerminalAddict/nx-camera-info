package main

import (
	"log"
	"nx-camera-info/internal/demodb"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: demo-database OUTPUT.sqlite (new file)")
	}
	if err := demodb.Create(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}
