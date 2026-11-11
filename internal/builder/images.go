package builder

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ListImages reads the registry and the artifact server. It is the
// read-only view of what is available: the image refs the registry holds and
// the qcow2 artifacts the artifact server serves. The engine-visible registry
// is used for the catalog; the artifact server is reachable from this
// container.
func (s *Service) ListImages() ([]string, []Artifact, error) {
	images, err := s.listImages()
	if err != nil {
		return nil, nil, err
	}
	artifacts, err := s.listArtifacts()
	if err != nil {
		return nil, nil, err
	}
	return images, artifacts, nil
}

// Artifact is one qcow2 on the artifact server, with its sha256.
type Artifact struct {
	Name     string
	Checksum string
}

func (s *Service) listImages() ([]string, error) {
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := getJSON("http://"+s.engineRegistry+"/v2/_catalog", &catalog); err != nil {
		return nil, err
	}
	var out []string
	for _, repo := range catalog.Repositories {
		var tags struct {
			Tags []string `json:"tags"`
		}
		if err := getJSON("http://"+s.engineRegistry+"/v2/"+repo+"/tags/list", &tags); err != nil {
			return nil, err
		}
		for _, tag := range tags.Tags {
			out = append(out, repo+":"+tag)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) listArtifacts() ([]Artifact, error) {
	var names []string
	if err := getJSON(s.artifactsURL+"/artifacts", &names); err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]Artifact, 0, len(names))
	for _, name := range names {
		sum, err := getText(s.artifactsURL + "/artifacts/" + name + "/sha256")
		if err != nil {
			return nil, err
		}
		out = append(out, Artifact{Name: name, Checksum: sum})
	}
	return out, nil
}

func getJSON(url string, out any) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func getText(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
