package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Legacy operation tests explicitly approve their fixture root and configured
// external directories. Production never issues a grant from this helper.
func callWithTestApproval(r *Registry, ctx context.Context, name string, data []byte) (string, error) {
	p, err := r.PrepareAccess(ctx, name, data)
	if err != nil {
		return "", err
	}
	if p != nil {
		for _, target := range p.paths {
			if target.noFollow {
				_, err = resolveAuthorizedPathNoFollowLeaf(ctx, p.root, target.input, p.safeDirs)
			} else {
				_, err = resolveAuthorizedPath(ctx, p.root, target.input, p.safeDirs)
			}
			if err != nil {
				return "", err
			}
		}
	}
	return r.Call(WithExecutionGrant(ctx, p), name, data)
}

func TestPreparedWriteRequiresExactGrant(t *testing.T) {
	root := t.TempDir()
	r := NewRegistry()
	require.NoError(t, RegisterFilesystem(r, FilesystemConfig{Root: root}))
	data := []byte(`{"path":"a.txt","content":"approved"}`)
	_, err := r.Call(context.Background(), "fs_write_file", data)
	require.ErrorIs(t, err, ErrMissingWriteGrant)
	p, err := r.PrepareAccess(context.Background(), "fs_write_file", data)
	require.NoError(t, err)
	ctx := WithExecutionGrant(context.Background(), p)
	_, err = resolveAuthorizedPath(ctx, root, "b.txt", nil)
	require.ErrorIs(t, err, ErrMissingWriteGrant, "a grant cannot borrow cwd authority for other paths")
	other, _ := json.Marshal(map[string]string{"path": "b.txt", "content": "unapproved"})
	_, err = r.Call(ctx, "fs_write_file", other)
	require.ErrorIs(t, err, ErrMissingWriteGrant)
	_, err = os.Stat(filepath.Join(root, "b.txt"))
	require.True(t, os.IsNotExist(err))
	_, err = r.Call(ctx, "fs_write_file", data)
	require.NoError(t, err)
}

func TestPreparedSecretContentCannotChangeTarget(t *testing.T) {
	root := t.TempDir()
	p, err := prepareFilesystemAccess(context.Background(), root, nil, "fs_write_file", []byte(`{"path":"file","content":"reference"}`))
	require.NoError(t, err)
	bound, err := p.BindResolvedArguments(context.Background(), []byte(`{"path":"file","content":"secret"}`))
	require.NoError(t, err)
	require.NoError(t, bound.Revalidate(context.Background()))
	_, err = p.BindResolvedArguments(context.Background(), []byte(`{"path":"other","content":"secret"}`))
	require.ErrorIs(t, err, ErrStaleAccess)
}

func TestPreparedWriteIncludesCreatedAncestors(t *testing.T) {
	root := t.TempDir()
	p, err := prepareFilesystemAccess(context.Background(), root, nil, "fs_write_file", []byte(`{"path":"new/nested/file","content":"x"}`))
	require.NoError(t, err)
	require.Contains(t, p.Intent.WriteDirs, filepath.Join(root, "new"))
}
