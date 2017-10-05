/*
Copyright 2017 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tika

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"time"

	"golang.org/x/net/context/ctxhttp"
)

// Server represents a Tika server. Create a new Server with NewServer,
// start it with Start, and shut it down with the close function returned
// from Start.
// There is no need to create a Server for an already running Tika Server
// since you can pass its URL directly to a Client.
type Server struct {
	jar  string
	url  string // url is derived from port.
	port string
}

// URL returns the URL of this Server.
func (s *Server) URL() string {
	return s.url
}

// NewServer creates a new Server. The default port is 9998.
func NewServer(jar, port string) (*Server, error) {
	if jar == "" {
		return nil, fmt.Errorf("no jar file specified")
	}
	if port == "" {
		port = "9998"
	}
	s := &Server{
		jar:  jar,
		port: port,
	}
	urlString := "http://localhost:" + s.port
	u, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("invalid port %q: %v", s.port, err)
	}
	s.url = u.String()
	return s, nil
}

var commandContext = exec.CommandContext

// Start starts the given server. Start will start a new Java process. The
// caller must call cancel() to shut down the process when finished with the
// Server. The given Context is used for the Java process.
func (s *Server) Start(ctx context.Context) (cancel func(), err error) {
	ctx, cancel = context.WithCancel(ctx)
	cmd := commandContext(ctx, "java", "-jar", s.jar, "-p", s.port)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	if err := s.waitForStart(ctx); err != nil {
		cancel()
		out, readErr := cmd.CombinedOutput()
		if readErr != nil {
			return nil, fmt.Errorf("error reading output: %v", readErr)
		}
		// Report stderr since sometimes the server says why it failed to start.
		return nil, fmt.Errorf("error starting server: %v\nserver stderr:\n\n%s", err, out)
	}
	return cancel, nil
}

// waitForServer waits until the given Server is responding to requests or
// ctx is Done().
func (s Server) waitForStart(ctx context.Context) error {
	c := NewClient(nil, s.url)
	for {
		select {
		case <-time.Tick(500 * time.Millisecond):
			if _, err := c.Version(ctx); err == nil {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func validateFileMD5(path, wantH string) (bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, ""
	}
	md5 := fmt.Sprintf("%x", h.Sum(nil))
	return md5 == wantH, md5
}

// A Version represents a Tika Server version.
type Version string

// Supported versions of Tika Server.
const (
	Version114 Version = "1.14"
	Version115 Version = "1.15"
	Version116 Version = "1.16"
)

var md5s = map[Version]string{
	Version114: "39055fc71358d774b9da066f80b1141c",
	Version115: "80bd3f00f05326d5190466de27d593dd",
	Version116: "6a549ce6ef6e186e019766059fd82fb2",
}

// DownloadServer downloads and validates the given server version,
// saving it at path. DownloadServer returns an error if it could
// not be downloaded/validated. Valid values for the version are 1.14.
// It is the caller's responsibility to remove the file when no longer needed.
// If the file already exists and has the correct MD5, DownloadServer will
// do nothing.
func DownloadServer(ctx context.Context, version Version, path string) error {
	wantH := md5s[version]
	if wantH == "" {
		return fmt.Errorf("unsupported Tika version: %s", version)
	}

	if _, err := os.Stat(path); err == nil {
		if ok, _ := validateFileMD5(path, wantH); ok {
			return nil
		}
	}
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("error creating file: %v", err)
	}
	defer out.Close()

	url := fmt.Sprintf("http://search.maven.org/remotecontent?filepath=org/apache/tika/tika-server/%s/tika-server-%s.jar", version, version)
	resp, err := ctxhttp.Get(ctx, nil, url)
	if err != nil {
		return fmt.Errorf("unable to download %q: %v", url, err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("error saving download: %v", err)
	}

	if ok, md5 := validateFileMD5(path, wantH); !ok {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("invalid md5: %s: error removing %s: %v", md5, path, err)
		}
		return fmt.Errorf("invalid md5: %s", md5)
	}
	return nil
}
