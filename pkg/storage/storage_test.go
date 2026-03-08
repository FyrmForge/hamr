package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errReader is an io.Reader that always returns the given error.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// ---------------------------------------------------------------------------
// Local storage tests
// ---------------------------------------------------------------------------

func TestNewLocalStorage_createsDirectory(t *testing.T) {
	dir := t.TempDir() + "/sub/dir"
	store, err := NewLocalStorage(dir)
	require.NoError(t, err)
	assert.DirExists(t, store.basePath)
}

func TestLocalStorage_SaveAndOpen(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	want := "hello, storage"
	require.NoError(t, store.Save(ctx, "greet.txt", strings.NewReader(want)))

	rc, err := store.Open(ctx, "greet.txt")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

func TestLocalStorage_SaveCreatesSubdirectories(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "a/b/c/file.txt", strings.NewReader("nested")))

	ok, err := store.Exists(ctx, "a/b/c/file.txt")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLocalStorage_Open_notFound(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	_, err = store.Open(context.Background(), "nope.txt")
	assert.Error(t, err)
}

func TestLocalStorage_Delete(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "del.txt", strings.NewReader("bye")))
	require.NoError(t, store.Delete(ctx, "del.txt"))

	ok, err := store.Exists(ctx, "del.txt")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestLocalStorage_Delete_notFound(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	// Idempotent: deleting a non-existent file returns nil.
	assert.NoError(t, store.Delete(context.Background(), "ghost.txt"))
}

func TestLocalStorage_Exists(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	ok, err := store.Exists(ctx, "missing.txt")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, store.Save(ctx, "present.txt", strings.NewReader("hi")))
	ok, err = store.Exists(ctx, "present.txt")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLocalStorage_pathTraversal(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()

	cases := []string{
		"../etc/passwd",
		"../../etc/shadow",
		"/etc/passwd",
		"foo/../../etc/passwd",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			err := store.Save(ctx, p, strings.NewReader("nope"))
			assert.Error(t, err, "Save should reject traversal path %q", p)

			_, err = store.Open(ctx, p)
			assert.Error(t, err, "Open should reject traversal path %q", p)

			_, err = store.Exists(ctx, p)
			assert.Error(t, err, "Exists should reject traversal path %q", p)

			err = store.Delete(ctx, p)
			assert.Error(t, err, "Delete should reject traversal path %q", p)
		})
	}
}

func TestLocalStorage_Save_atomicOnFailure(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()

	// A reader that returns an error after producing some bytes.
	failReader := io.MultiReader(
		strings.NewReader("partial-data"),
		errReader{errors.New("disk full")},
	)

	err = store.Save(ctx, "should-not-exist.txt", failReader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")

	// The target file must not exist — no partial leftovers.
	ok, err := store.Exists(ctx, "should-not-exist.txt")
	require.NoError(t, err)
	assert.False(t, ok, "partial file should not remain after failed write")
}

func TestLocalStorage_Save_atomicPreservesOld(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "data.txt", strings.NewReader("original")))

	// Overwrite attempt that fails mid-write.
	failReader := io.MultiReader(
		strings.NewReader("new-partial"),
		errReader{errors.New("oops")},
	)
	err = store.Save(ctx, "data.txt", failReader)
	require.Error(t, err)

	// Original content must be preserved.
	rc, err := store.Open(ctx, "data.txt")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "original", string(got))
}

func TestLocalStorage_overwrite(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "f.txt", strings.NewReader("v1")))
	require.NoError(t, store.Save(ctx, "f.txt", strings.NewReader("v2")))

	rc, err := store.Open(ctx, "f.txt")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "v2", string(got))
}

// ---------------------------------------------------------------------------
// Local storage List tests
// ---------------------------------------------------------------------------

func TestLocalStorage_List_empty(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	got, err := store.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLocalStorage_List_allFiles(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "a.txt", strings.NewReader("a")))
	require.NoError(t, store.Save(ctx, "b.txt", strings.NewReader("b")))
	require.NoError(t, store.Save(ctx, "sub/c.txt", strings.NewReader("c")))

	got, err := store.List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, got, 3)
	assert.Contains(t, got, "a.txt")
	assert.Contains(t, got, "b.txt")
	assert.Contains(t, got, filepath.Join("sub", "c.txt"))
}

func TestLocalStorage_List_withPrefix(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Save(ctx, "photos/a.jpg", strings.NewReader("a")))
	require.NoError(t, store.Save(ctx, "photos/b.jpg", strings.NewReader("b")))
	require.NoError(t, store.Save(ctx, "docs/c.txt", strings.NewReader("c")))

	got, err := store.List(ctx, "photos")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Contains(t, got, filepath.Join("photos", "a.jpg"))
	assert.Contains(t, got, filepath.Join("photos", "b.jpg"))
}

func TestLocalStorage_List_missingPrefix(t *testing.T) {
	store, err := NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	got, err := store.List(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// ---------------------------------------------------------------------------
// S3 storage tests (mocked)
// ---------------------------------------------------------------------------

// mockS3Client implements s3API for testing.
type mockS3Client struct {
	putFn          func(ctx context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	getFn          func(ctx context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	deleteFn       func(ctx context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)
	headFn         func(ctx context.Context, in *s3.HeadObjectInput) (*s3.HeadObjectOutput, error)
	listFn         func(ctx context.Context, in *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error)
	createBucketFn func(ctx context.Context, in *s3.CreateBucketInput) (*s3.CreateBucketOutput, error)
	headBucketFn   func(ctx context.Context, in *s3.HeadBucketInput) (*s3.HeadBucketOutput, error)
}

func (m *mockS3Client) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return m.putFn(ctx, in)
}

func (m *mockS3Client) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return m.getFn(ctx, in)
}

func (m *mockS3Client) DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return m.deleteFn(ctx, in)
}

func (m *mockS3Client) HeadObject(ctx context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return m.headFn(ctx, in)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listFn != nil {
		return m.listFn(ctx, in)
	}
	return &s3.ListObjectsV2Output{}, nil
}

func (m *mockS3Client) CreateBucket(ctx context.Context, in *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	if m.createBucketFn != nil {
		return m.createBucketFn(ctx, in)
	}
	return &s3.CreateBucketOutput{}, nil
}

func (m *mockS3Client) HeadBucket(ctx context.Context, in *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if m.headBucketFn != nil {
		return m.headBucketFn(ctx, in)
	}
	return &s3.HeadBucketOutput{}, nil
}

func (m *mockS3Client) PutBucketPolicy(_ context.Context, _ *s3.PutBucketPolicyInput, _ ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	return &s3.PutBucketPolicyOutput{}, nil
}

// mockPresigner implements presigner for testing.
type mockPresigner struct {
	fn func(ctx context.Context, in *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error)
}

func (m *mockPresigner) PresignGetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return m.fn(ctx, in)
}

func newTestS3(mc *mockS3Client, mp *mockPresigner) *S3Storage {
	return newS3StorageWithClient(mc, mp, "test-bucket")
}

// --- Save ---

func TestS3Storage_Save(t *testing.T) {
	mc := &mockS3Client{
		putFn: func(_ context.Context, in *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			assert.Equal(t, "test-bucket", *in.Bucket)
			assert.Equal(t, "photos/cat.jpg", *in.Key)
			return &s3.PutObjectOutput{}, nil
		},
	}
	store := newTestS3(mc, nil)
	err := store.Save(context.Background(), "photos/cat.jpg", strings.NewReader("data"))
	assert.NoError(t, err)
}

func TestS3Storage_Save_error(t *testing.T) {
	mc := &mockS3Client{
		putFn: func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return nil, errors.New("network error")
		},
	}
	store := newTestS3(mc, nil)
	err := store.Save(context.Background(), "key", strings.NewReader("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network error")
}

// --- Open ---

func TestS3Storage_Open(t *testing.T) {
	body := io.NopCloser(bytes.NewReader([]byte("file-data")))
	mc := &mockS3Client{
		getFn: func(_ context.Context, in *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			assert.Equal(t, "docs/readme.md", *in.Key)
			return &s3.GetObjectOutput{Body: body}, nil
		},
	}
	store := newTestS3(mc, nil)
	rc, err := store.Open(context.Background(), "docs/readme.md")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "file-data", string(got))
}

func TestS3Storage_Open_error(t *testing.T) {
	mc := &mockS3Client{
		getFn: func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
			return nil, errors.New("access denied")
		},
	}
	store := newTestS3(mc, nil)
	_, err := store.Open(context.Background(), "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

// --- Delete ---

func TestS3Storage_Delete(t *testing.T) {
	mc := &mockS3Client{
		deleteFn: func(_ context.Context, in *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
			assert.Equal(t, "old.txt", *in.Key)
			return &s3.DeleteObjectOutput{}, nil
		},
	}
	store := newTestS3(mc, nil)
	assert.NoError(t, store.Delete(context.Background(), "old.txt"))
}

func TestS3Storage_Delete_error(t *testing.T) {
	mc := &mockS3Client{
		deleteFn: func(context.Context, *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	store := newTestS3(mc, nil)
	err := store.Delete(context.Background(), "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "throttled")
}

// --- Exists ---

func TestS3Storage_Exists_true(t *testing.T) {
	mc := &mockS3Client{
		headFn: func(context.Context, *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
			return &s3.HeadObjectOutput{}, nil
		},
	}
	store := newTestS3(mc, nil)
	ok, err := store.Exists(context.Background(), "found.txt")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestS3Storage_Exists_false(t *testing.T) {
	mc := &mockS3Client{
		headFn: func(context.Context, *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
			return nil, &s3types.NotFound{}
		},
	}
	store := newTestS3(mc, nil)
	ok, err := store.Exists(context.Background(), "missing.txt")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestS3Storage_Exists_error(t *testing.T) {
	mc := &mockS3Client{
		headFn: func(context.Context, *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
			return nil, errors.New("timeout")
		},
	}
	store := newTestS3(mc, nil)
	_, err := store.Exists(context.Background(), "key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

// --- SignURL ---

func TestS3Storage_SignURL(t *testing.T) {
	mp := &mockPresigner{
		fn: func(_ context.Context, in *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
			assert.Equal(t, "img.png", *in.Key)
			return &v4.PresignedHTTPRequest{URL: "https://example.com/signed"}, nil
		},
	}
	store := newTestS3(&mockS3Client{}, mp)
	url, err := store.SignURL(context.Background(), "img.png", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/signed", url)
}

func TestS3Storage_SignURL_error(t *testing.T) {
	mp := &mockPresigner{
		fn: func(context.Context, *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
			return nil, errors.New("cred error")
		},
	}
	store := newTestS3(&mockS3Client{}, mp)
	_, err := store.SignURL(context.Background(), "key", time.Minute)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cred error")
}

// --- List ---

func TestS3Storage_List(t *testing.T) {
	key1, key2 := "photos/a.jpg", "photos/b.jpg"
	mc := &mockS3Client{
		listFn: func(_ context.Context, in *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error) {
			assert.Equal(t, "test-bucket", *in.Bucket)
			assert.Equal(t, "photos/", *in.Prefix)
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: &key1},
					{Key: &key2},
				},
			}, nil
		},
	}
	store := newTestS3(mc, nil)
	got, err := store.List(context.Background(), "photos/")
	require.NoError(t, err)
	assert.Equal(t, []string{"photos/a.jpg", "photos/b.jpg"}, got)
}

func TestS3Storage_List_empty(t *testing.T) {
	mc := &mockS3Client{
		listFn: func(context.Context, *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{}, nil
		},
	}
	store := newTestS3(mc, nil)
	got, err := store.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestS3Storage_List_error(t *testing.T) {
	mc := &mockS3Client{
		listFn: func(context.Context, *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error) {
			return nil, errors.New("access denied")
		},
	}
	store := newTestS3(mc, nil)
	_, err := store.List(context.Background(), "prefix")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

// --- NewS3Storage validation ---

func TestNewS3Storage_emptyBucket(t *testing.T) {
	_, err := NewS3Storage(S3Config{Region: "us-east-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}

func TestNewS3Storage_emptyRegion(t *testing.T) {
	_, err := NewS3Storage(S3Config{Bucket: "my-bucket"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")
}
