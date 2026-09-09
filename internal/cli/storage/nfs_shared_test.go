package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestStorageCreateNFSOmitsIntrinsicShared(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{})
	_, err := run(t, f, "create", "--storage", "nfs-test", "--type", "nfs", "--server", "10.0.0.1", "--export", "/test", "--shared", "--nodes", "node1", "--options", "vers=4.1,hard")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.False(t, rec.form.Has("shared"))
	require.Equal(t, "10.0.0.1", rec.form.Get("server"))
	require.Equal(t, "/test", rec.form.Get("export"))
	require.Equal(t, "node1", rec.form.Get("nodes"))
	require.Equal(t, "vers=4.1,hard", rec.form.Get("options"))
}

func TestStorageSharedFalseIsNotSilentlyDiscarded(t *testing.T) {
	for _, kind := range []string{"nfs", "dir", "lvm"} {
		t.Run(kind, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordJSON(f, "POST /api2/json/storage", &rec, map[string]any{})
			_, err := run(t, f, "create", "--storage", "test", "--type", kind, "--shared=false")
			if kind == "nfs" {
				require.ErrorContains(t, err, "inherently shared")
				require.Empty(t, rec.method)
			} else {
				require.NoError(t, err)
				require.Equal(t, "0", rec.form.Get("shared"))
			}
		})
	}
}

func TestStorageSetSharedUsesObservedType(t *testing.T) {
	for _, kind := range []string{"nfs", "dir", "lvm", ""} {
		t.Run(kind, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			f.HandleFunc("GET /api2/json/storage/test", func(w http.ResponseWriter, _ *http.Request) { testhelper.WriteData(w, map[string]any{"type": kind}) })
			recordJSON(f, "PUT /api2/json/storage/test", &rec, map[string]any{})
			_, err := run(t, f, "set", "test", "--shared", "--nodes", "node1")
			if kind == "" {
				require.ErrorContains(t, err, "type unavailable")
				require.Empty(t, rec.method)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "node1", rec.form.Get("nodes"))
			require.Equal(t, kind != "nfs", rec.form.Has("shared"))
			if kind != "nfs" {
				require.Equal(t, "1", rec.form.Get("shared"))
			}
		})
	}
}

func TestStorageSetNFSRefusesFalseBeforeUpdate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/storage/test", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"type": "nfs"})
	})
	recordJSON(f, "PUT /api2/json/storage/test", &rec, map[string]any{})
	_, err := run(t, f, "set", "test", "--shared=false")
	require.ErrorContains(t, err, "inherently shared")
	require.Empty(t, rec.method)
}

func TestStorageSetNFSOnlySharedSendsEmptyUpdate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/storage/test", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"type": "nfs"})
	})
	recordJSON(f, "PUT /api2/json/storage/test", &rec, map[string]any{"storage": "test", "type": "nfs"})
	_, err := run(t, f, "set", "test", "--shared")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Empty(t, rec.form)
}
