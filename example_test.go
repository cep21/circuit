package hystrix_test

import (
	"github.com/cep21/hystrix"
	"context"
	"net/http"
	"errors"
	"fmt"
	"io"
	"bytes"
	"time"
)

func Example_http() {
	h := hystrix.Hystrix{}
	c := h.MustCreateCircuit("hello-http", hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			Timeout: time.Second * 3,
		},
	})

	var body bytes.Buffer
	runErr := c.Run(context.Background(), func (ctx context.Context) error {
		req, err := http.NewRequest("GET", "http://www.google.com", nil)
		if err != nil {
			return hystrix.SimpleBadRequest{Err: err}
		}
		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			return hystrix.SimpleBadRequest{Err: errors.New("server found your request invalid")}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("invalid status code: %d", resp.StatusCode)
		}
		if _, err := io.Copy(&body, resp.Body); err != nil {
			return err
		}
		return resp.Body.Close()
	})
	if runErr == nil {
		fmt.Printf("We saw a body\n")
		return
	}
	fmt.Printf("There was an error with the request: %s\n", runErr)
	// Output: We saw a body
}
