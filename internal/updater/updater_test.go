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

package updater

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoCheckForUpdate(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		serverResponse string
		serverStatus   int
		wantVersion    string
		wantErr        bool
	}{
		{
			name:           "update available",
			currentVersion: "v1.0.0",
			serverResponse: `{"tag_name": "v2.0.0"}`,
			serverStatus:   http.StatusOK,
			wantVersion:    "v2.0.0",
			wantErr:        false,
		},
		{
			name:           "up to date",
			currentVersion: "v1.0.0",
			serverResponse: `{"tag_name": "v1.0.0"}`,
			serverStatus:   http.StatusOK,
			wantVersion:    "",
			wantErr:        false,
		},
		{
			name:           "dev build",
			currentVersion: "dev",
			serverResponse: `{"tag_name": "v1.0.0"}`,
			serverStatus:   http.StatusOK,
			wantVersion:    "",
			wantErr:        true,
		},
		{
			name:           "server error",
			currentVersion: "v1.0.0",
			serverResponse: "",
			serverStatus:   http.StatusInternalServerError,
			wantVersion:    "",
			wantErr:        true,
		},
		{
			name:           "invalid json",
			currentVersion: "v1.0.0",
			serverResponse: "not json",
			serverStatus:   http.StatusOK,
			wantVersion:    "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverStatus)
				w.Write([]byte(tt.serverResponse))
			}))
			defer server.Close()

			gotVersion, err := doCheckForUpdate(server.URL, tt.currentVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("doCheckForUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotVersion != tt.wantVersion {
				t.Errorf("doCheckForUpdate() = %v, want %v", gotVersion, tt.wantVersion)
			}
		})
	}
}
