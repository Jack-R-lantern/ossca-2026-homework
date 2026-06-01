//go:build linux

package main

func ensureVethNamesAvailable(handles netlinkHandles, cfg vethPairConfig) error {
	if err := ensureLinkAbsent(handles.host, cfg.HostIfName); err != nil {
		return err
	}

	if err := ensureLinkAbsent(handles.ns, cfg.PeerIfName); err != nil {
		return err
	}

	if err := ensureLinkAbsent(handles.host, cfg.TempPeerName); err != nil {
		return err
	}

	if err := ensureLinkAbsent(handles.ns, cfg.TempPeerName); err != nil {
		return err
	}

	return nil
}
