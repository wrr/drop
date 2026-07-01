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
	"reflect"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestCreateSpec(t *testing.T) {
	tests := []struct {
		name     string
		uid      int
		gid      int
		terminal bool
	}{
		{name: "root user", uid: 0, gid: 0, terminal: true},
		{name: "non-root user", uid: 1000, gid: 2000, terminal: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ContainerConfig{
				FsRootDst:  "/root-dst",
				FsRootSrc:  "/root-src",
				Args:       []string{"/bin/sh"},
				Env:        []string{"PATH=/usr/bin", "HOME=/home/al"},
				Cwd:        "/home/al",
				UID:        tt.uid,
				GID:        tt.gid,
				Terminal:   tt.terminal,
				BindMounts: []string{"/home/al", "/tmp"},
			}

			out, err := CreateSpec(cfg)
			if err != nil {
				t.Fatalf("CreateSpec: %v", err)
			}

			var spec specs.Spec
			if err := json.Unmarshal([]byte(out), &spec); err != nil {
				t.Fatalf("unmarshal spec: %v", err)
			}

			if spec.Root.Path != cfg.FsRootDst {
				t.Errorf("Root.Path = %q, want %q", spec.Root.Path, cfg.FsRootDst)
			}
			if spec.Process.User.UID != uint32(tt.uid) {
				t.Errorf("Process.User.UID = %d, want %d", spec.Process.User.UID, tt.uid)
			}
			if spec.Process.User.GID != uint32(tt.gid) {
				t.Errorf("Process.User.GID = %d, want %d", spec.Process.User.GID, tt.gid)
			}
			if spec.Process.Terminal != tt.terminal {
				t.Errorf("Process.Terminal = %v, want %v", spec.Process.Terminal, tt.terminal)
			}
			if !reflect.DeepEqual(spec.Process.Env, cfg.Env) {
				t.Errorf("Process.Env = %v, want %v", spec.Process.Env, cfg.Env)
			}
			if !spec.Process.NoNewPrivileges {
				t.Error("Process.NoNewPrivileges = false, want true")
			}

			wantMounts := []specs.Mount{
				{Destination: "/home/al", Source: "/root-src/home/al", Type: "bind", Options: []string{"rbind", "nosuid", "nodev"}},
				{Destination: "/tmp", Source: "/root-src/tmp", Type: "bind", Options: []string{"rbind", "nosuid", "nodev"}},
			}
			if !reflect.DeepEqual(spec.Mounts, wantMounts) {
				t.Errorf("Mounts = %+v, want %+v", spec.Mounts, wantMounts)
			}

			if tt.uid == 0 {
				if spec.Linux != nil {
					t.Errorf("Linux = %+v, want nil", spec.Linux)
				}
				return
			}

			if spec.Linux == nil {
				t.Fatal("Linux = nil, want user namespace config")
			}
			wantUIDMappings := []specs.LinuxIDMapping{
				{ContainerID: uint32(tt.uid), HostID: uint32(tt.uid), Size: 1},
			}
			wantGIDMappings := []specs.LinuxIDMapping{
				{ContainerID: uint32(tt.gid), HostID: uint32(tt.gid), Size: 1},
			}
			if !reflect.DeepEqual(spec.Linux.UIDMappings, wantUIDMappings) {
				t.Errorf("UIDMappings = %+v, want %+v", spec.Linux.UIDMappings, wantUIDMappings)
			}
			if !reflect.DeepEqual(spec.Linux.GIDMappings, wantGIDMappings) {
				t.Errorf("GIDMappings = %+v, want %+v", spec.Linux.GIDMappings, wantGIDMappings)
			}
		})
	}
}
