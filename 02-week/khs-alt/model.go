package main

type Input struct {
	Name string `json:"name"`
}

type Output struct {
	Name      string `json:"name"`
	NetNSPath string `json:"netns_path"`
}

type VethInput struct {
	HostIfname string `json:"host_ifname"`
	PeerIfname string `json:"peer_ifname"`
	HostIP     string `json:"host_ip"`
	PeerIP     string `json:"peer_ip"`
}

type ExecInput struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type ExecOutput struct {
	Name      string `json:"name"`
	ParentPid int    `json:"parent_pid"`
	ChildPid  int    `json:"child_pid"`
}

func (i *Input) Validate() error {

	return nil
}

func (i *VethInput) Validate() error {

	return nil
}

func (i *ExecInput) Validate() error {

	return nil
}
