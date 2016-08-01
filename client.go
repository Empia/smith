package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"golang.org/x/net/context"
)

const (
	// maxResponseSize is the maximum response size we are willing to read.
	maxResponseSize = 10 * 1024 * 1024
	// maxStatusResponseSize is the maximum status response size we are willing to read.
	maxStatusResponseSize = 10 * 1024
)

type StreamHandler func(io.ReadCloser) error

type ResponseHandler func(*http.Response) error

type ResultFactory func() interface{}

type WatchFactory func() (request interface{}, args url.Values)

type ResourceClient struct {
	Scheme      string
	HostPort    string
	BearerToken string
	Client      http.Client
}

type StatusError struct {
	msg    string
	status Status
}

func (se *StatusError) Error() string {
	return se.msg
}

func (se *StatusError) Status() Status {
	return se.status
}

func (c *ResourceClient) List(ctx context.Context, groupVersion, namespace, resource, name string, args url.Values, request interface{}, into interface{}) error {
	return c.Do(ctx, "GET", groupVersion, namespace, resource, name, args, http.StatusOK, request, into)
}

func (c *ResourceClient) Create(ctx context.Context, groupVersion, namespace, resource string, request interface{}, response interface{}) error {
	return c.Do(ctx, "POST", groupVersion, namespace, resource, "", nil, http.StatusCreated, request, response)
}

func (c *ResourceClient) Update(ctx context.Context, groupVersion, namespace, resource, name string, request interface{}, response interface{}) error {
	return c.Do(ctx, "PUT", groupVersion, namespace, resource, name, nil, http.StatusOK, request, response)
}

func (c *ResourceClient) Delete(ctx context.Context, groupVersion, namespace, resource, name string, request interface{}, response interface{}) error {
	return c.Do(ctx, "DELETE", groupVersion, namespace, resource, name, nil, http.StatusOK, request, response)
}

func (c *ResourceClient) Watch(ctx context.Context, groupVersion, namespace, resource, name string, rf ResultFactory, wf WatchFactory) <-chan interface{} {
	results := make(chan interface{})
	go func() {
		defer close(results)
		for {
			request, args := wf()
			args.Set("watch", "true")
			err := c.DoCheckResponse(ctx, "GET", groupVersion, namespace, resource, name, args, http.StatusOK, request, func(r io.ReadCloser) error {
				decoder := json.NewDecoder(r)
				for {
					r := rf()
					if err := decoder.Decode(r); err != nil {
						return err
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case results <- r:
					}
				}
			})
			if err != nil {
				results <- err
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
			}
			// Delay after failed/closed connection
			select {
			case <-ctx.Done():
				results <- ctx.Err()
				return
			case <-time.After(1 * time.Second):
			}
		}
	}()
	return results
}

func (c *ResourceClient) Do(ctx context.Context, verb, groupVersion, namespace, resource, name string, args url.Values, expectedStatus int, request interface{}, response interface{}) error {
	return c.DoCheckResponse(ctx, verb, groupVersion, namespace, resource, name, args, expectedStatus, request, func(r io.ReadCloser) error {
		// Consume body even if "response" is nil to enable connection reuse
		b, err := ioutil.ReadAll(io.LimitReader(r, maxResponseSize))
		if err != nil {
			return err
		}
		log.Printf("Server response:\n%s", b)
		if response == nil {
			return nil
		}
		return json.Unmarshal(b, response)
	})
}

func (c *ResourceClient) DoCheckResponse(ctx context.Context, verb, groupVersion, namespace, resource, name string, args url.Values, expectedStatus int, request interface{}, f StreamHandler) error {
	return c.DoRequest(ctx, verb, groupVersion, namespace, resource, name, args, request, func(resp *http.Response) error {
		if resp.StatusCode != expectedStatus {
			return func() error {
				msg := fmt.Sprintf("received bad status code %d", resp.StatusCode)
				b, err := ioutil.ReadAll(io.LimitReader(resp.Body, maxStatusResponseSize))
				if err != nil {
					return errors.New(msg)
				}
				se := StatusError{
					msg: msg,
				}
				log.Printf("Unexpected server response: %d\n%s", resp.StatusCode, b)
				if json.Unmarshal(b, &se.status) != nil {
					return errors.New(msg)
				}
				return &se
			}()
		}
		return f(resp.Body)
	})
}

func (c *ResourceClient) DoRequest(ctx context.Context, verb, groupVersion, namespace, resource, name string, args url.Values, request interface{}, f ResponseHandler) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	p := []string{DefaultAPIPath, groupVersion}
	if namespace != "" {
		p = append(p, "namespaces", namespace)
	}
	p = append(p, resource, name)
	url := url.URL{
		Scheme:   c.Scheme,
		Host:     c.HostPort,
		Path:     path.Join(p...),
		RawQuery: args.Encode(),
	}
	req, err := http.NewRequest(verb, url.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("unable to create http.Request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "smith/"+Version+"/"+GitCommit)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.BearerToken))
	// TODO replace this with request built-in context when Go 1.7 arrives
	ctxReq, cancelFunc := context.WithCancel(ctx) // Separate context to release goroutine when function returns
	defer cancelFunc()
	go func() {
		<-ctxReq.Done()
		close(req.Cancel)
	}()
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()
	return f(resp)
}

func NewInCluster() (*ResourceClient, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if len(host) == 0 || len(port) == 0 {
		return nil, errors.New("unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined")
	}
	token, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/" + ServiceAccountTokenKey)
	if err != nil {
		return nil, err
	}
	rootCA := "/var/run/secrets/kubernetes.io/serviceaccount/" + ServiceAccountRootCAKey
	CAData, err := ioutil.ReadFile(rootCA)
	if err != nil {
		return nil, err
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(CAData) {
		log.Printf("Failed to add certificate from %s", rootCA)
	}
	return &ResourceClient{
		Scheme:      "https",
		HostPort:    net.JoinHostPort(host, port),
		BearerToken: string(token),
		Client: http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				TLSHandshakeTimeout: 10 * time.Second,
				TLSClientConfig: &tls.Config{
					// Can't use SSLv3 because of POODLE and BEAST
					// Can't use TLSv1.0 because of POODLE and BEAST using CBC cipher
					// Can't use TLSv1.1 because of RC4 cipher usage
					MinVersion: tls.VersionTLS12,
					RootCAs:    certPool,
					//InsecureSkipVerify: true,
				},
				Dial: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).Dial,
			},
		},
	}, nil
}