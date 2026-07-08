// Copyright 2026 Jan Wrobel <jan@mixedbit.org>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gvisor

import (
	"encoding/json"
	"os"
	"path/filepath"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type ContainerConfig struct {
	FsRootDst  string
	FsRootSrc  string
	Args       []string
	Env        []string
	Cwd        string
	UID        int
	GID        int
	Terminal   bool
	BindMounts []string
}

// CreateSpec builds an OCI runtime spec for running the container
// described by cfg under gVisor and returns it as a config.json
// string.
//
// When the container is run with non-zero UID, it creates its own
// nested user namespace to work-around gVisor limitation:
// https://github.com/google/gvisor/issues/13564
func CreateSpec(cfg ContainerConfig) (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	noCaps := &specs.LinuxCapabilities{
		Bounding:    []string{},
		Effective:   []string{},
		Inheritable: []string{},
		Permitted:   []string{},
		Ambient:     []string{},
	}
	spec := &specs.Spec{
		Version:  specs.Version,
		Hostname: hostname,
		Root:     &specs.Root{Path: cfg.FsRootDst, Readonly: true},
		Process: &specs.Process{
			Terminal:        cfg.Terminal,
			User:            specs.User{UID: uint32(cfg.UID), GID: uint32(cfg.GID)},
			Args:            cfg.Args,
			Env:             cfg.Env,
			Cwd:             cfg.Cwd,
			Capabilities:    noCaps,
			NoNewPrivileges: true,
		},
		Mounts: containerMounts(cfg.FsRootSrc, cfg.BindMounts),
	}
	if cfg.UID != 0 || cfg.GID != 0 {
		idMapping := func(id int) specs.LinuxIDMapping {
			return specs.LinuxIDMapping{
				ContainerID: uint32(id),
				HostID:      uint32(id),
				Size:        1,
			}
		}
		spec.Linux = &specs.Linux{
			Namespaces: []specs.LinuxNamespace{
				{Type: specs.UserNamespace},
			},
			UIDMappings: []specs.LinuxIDMapping{idMapping(cfg.UID)},
			GIDMappings: []specs.LinuxIDMapping{idMapping(cfg.GID)},
		}
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func containerMounts(fsRootSrc string, bindMounts []string) []specs.Mount {
	mounts := make([]specs.Mount, 0, len(bindMounts))
	for _, dst := range bindMounts {
		mounts = append(mounts, specs.Mount{
			Destination: dst,
			Source:      filepath.Join(fsRootSrc, dst),
			Type:        "bind",
			Options:     []string{"rbind", "nosuid", "nodev"},
		})
	}
	// gVisor automatically mounts /proc, /sys, and /dev, these mounts
	// don't need to be included in the JSON spec.
	return mounts
}
