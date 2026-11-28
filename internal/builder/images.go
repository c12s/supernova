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
// read-only view of what is available: the image refs the registry holds,
// each with its manifest digest (the value `image update --digest` takes),
// and the qcow2 artifacts the artifact server serves. The engine-visible
// registry is used for the catalog; the artifact server is reachable from
// this container.
func (s *Service) ListImages() ([]Image, []Artifact, error) {
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

// Image is one tag in the registry with the digest it currently points to.
type Image struct {
	Ref    string
	Digest string
}

// Artifact is one qcow2 on the artifact server, with its sha256.
type Artifact struct {
	Name     string
	Checksum string
}

func (s *Service) listImages() ([]Image, error) {
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := getJSON("http://"+s.engineRegistry+"/v2/_catalog", &catalog); err != nil {
		return nil, err
	}
	var out []Image
	for _, repo := range catalog.Repositories {
		var tags struct {
			Tags []string `json:"tags"`
		}
		if err := getJSON("http://"+s.engineRegistry+"/v2/"+repo+"/tags/list", &tags); err != nil {
			return nil, err
		}
		for _, tag := range tags.Tags {
			digest, err := s.manifestDigest(repo, tag)
			if err != nil {
				return nil, err
			}
			out = append(out, Image{Ref: repo + ":" + tag, Digest: digest})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// manifestDigest asks the registry which manifest a tag points to. The
// digest comes from the Docker-Content-Digest header of a HEAD request; the
// Accept header must name the OCI manifest type, or Zot answers 404 for
// images pushed as OCI.
func (s *Service) manifestDigest(repo, tag string) (string, error) {
	url := "http://" + s.engineRegistry + "/v2/" + repo + "/manifests/" + tag
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("HEAD %s: no Docker-Content-Digest header", url)
	}
	return digest, nil
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
