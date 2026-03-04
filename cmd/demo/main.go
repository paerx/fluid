package main

import (
	"github.com/paerx/fluid/pkg/fluid"
	"log"
	"math/rand"
	"time"
)

func main() {
	handle, err := fluid.Goroutine(
		fluid.WithAuthToken("123123"),
		fluid.WithSnapshotInterval(12*time.Second),
		fluid.WithUI(true),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	log.Printf("FLUID demo at http://%s%s", handle.Addr, handle.BasePath)

	go workload()
	select {}
}

func workload() {
	for {
		n := 10 + rand.Intn(10)
		for i := 0; i < n; i++ {
			go func(id int) {
				delay := time.Duration(40+rand.Intn(220)) * time.Millisecond
				timer := time.NewTimer(delay)
				defer timer.Stop()
				<-timer.C
			}(i)
		}
		buf := make([]byte, 256*1024+rand.Intn(512*1024))
		for i := range buf {
			buf[i] = byte(i % 255)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
