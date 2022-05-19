package signaling

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func StartHttpSdpServer(addr string) chan string {
	sdpChan := make(chan string)

	router := httprouter.New()
	router.GET("/sdp", func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "done")
		sdpChan <- string(body)
	})

	go func() {
		if err := http.ListenAndServe(addr, router); err != nil {
			log.Fatal(err)
		}
	}()

	return sdpChan
}
