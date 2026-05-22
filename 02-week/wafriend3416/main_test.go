package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeService struct {
	createNetnsName string
	createNetnsPath string
	createNetnsErr  error
	vethName        string
	vethReq         vethRequest
	vethResp        vethResponse
	vethErr         error
	execName        string
	execReq         execRequest
	execResp        execResponse
	execErr         error
}

func (f *fakeService) CreateNetns(name string) (string, error) {
	f.createNetnsName = name
	if f.createNetnsErr != nil {
		return "", f.createNetnsErr
	}

	return f.createNetnsPath, nil
}

func (f *fakeService) CreateVeth(name string, req vethRequest) (vethResponse, error) {
	f.vethName = name
	f.vethReq = req
	if f.vethErr != nil {
		return vethResponse{}, f.vethErr
	}

	return f.vethResp, nil
}

func (f *fakeService) ExecInNetns(name string, req execRequest) (execResponse, error) {
	f.execName = name
	f.execReq = req
	if f.execErr != nil {
		return execResponse{}, f.execErr
	}

	return f.execResp, nil
}

func TestHandleNetnsRequiresPost(t *testing.T) {
	svc := &fakeService{}
	req := httptest.NewRequest(http.MethodGet, "/netns", nil)
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleNetnsRejectsInvalidJSON(t *testing.T) {
	svc := &fakeService{}
	req := httptest.NewRequest(http.MethodPost, "/netns", bytes.NewBufferString("{"))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleNetnsRejectsInvalidName(t *testing.T) {
	svc := &fakeService{}
	req := httptest.NewRequest(http.MethodPost, "/netns", bytes.NewBufferString(`{"name":"../bad"}`))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleNetnsCreatesNamespace(t *testing.T) {
	svc := &fakeService{createNetnsPath: "/var/run/netns/test-01"}
	req := httptest.NewRequest(http.MethodPost, "/netns", bytes.NewBufferString(`{"name":"test-01"}`))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if svc.createNetnsName != "test-01" {
		t.Fatalf("CreateNetns name=%q, want %q", svc.createNetnsName, "test-01")
	}

	var resp createNetnsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Name != "test-01" || resp.NetnsPath != "/var/run/netns/test-01" {
		t.Fatalf("response=%+v", resp)
	}
}

func TestHandleNetnsPropagatesServiceError(t *testing.T) {
	svc := &fakeService{createNetnsErr: errors.New("boom")}
	req := httptest.NewRequest(http.MethodPost, "/netns", bytes.NewBufferString(`{"name":"test-01"}`))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleVethRejectsMissingFields(t *testing.T) {
	svc := &fakeService{}
	req := httptest.NewRequest(http.MethodPost, "/netns/test-01/veth", bytes.NewBufferString(`{"host_ifname":"veth0"}`))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleVethCreatesPair(t *testing.T) {
	svc := &fakeService{
		vethResp: vethResponse{
			Name:       "test-01",
			HostIfname: "veth-test01",
			PeerIfname: "eth0",
			HostIP:     "10.10.0.1/24",
			PeerIP:     "10.10.0.2/24",
			NetnsPath:  "/var/run/netns/test-01",
		},
	}
	body := `{"host_ifname":"veth-test01","peer_ifname":"eth0","host_ip":"10.10.0.1/24","peer_ip":"10.10.0.2/24"}`
	req := httptest.NewRequest(http.MethodPost, "/netns/test-01/veth", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if svc.vethName != "test-01" {
		t.Fatalf("CreateVeth name=%q, want %q", svc.vethName, "test-01")
	}

	if svc.vethReq.HostIfname != "veth-test01" ||
		svc.vethReq.PeerIfname != "eth0" ||
		svc.vethReq.HostIP != "10.10.0.1/24" ||
		svc.vethReq.PeerIP != "10.10.0.2/24" {
		t.Fatalf("CreateVeth request=%+v", svc.vethReq)
	}

	var resp vethResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp != svc.vethResp {
		t.Fatalf("response=%+v, want %+v", resp, svc.vethResp)
	}
}

func TestHandleVethPropagatesServiceError(t *testing.T) {
	svc := &fakeService{vethErr: errors.New("boom")}
	body := `{"host_ifname":"veth-test01","peer_ifname":"eth0","host_ip":"10.10.0.1/24","peer_ip":"10.10.0.2/24"}`
	req := httptest.NewRequest(http.MethodPost, "/netns/test-01/veth", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	newHandler(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
