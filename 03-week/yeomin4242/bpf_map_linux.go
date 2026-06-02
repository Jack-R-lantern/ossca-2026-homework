//go:build linux

package main

import (
	"errors"

	"github.com/cilium/ebpf"
)

func clearMap(m *ebpf.Map) error {
	var key uint32
	var next uint32

	err := m.NextKey(nil, &next)
	for err == nil {
		key = next
		err = m.NextKey(key, &next)
		if deleteErr := m.Delete(key); deleteErr != nil && !errors.Is(deleteErr, ebpf.ErrKeyNotExist) {
			return deleteErr
		}
	}

	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}

	return err
}
