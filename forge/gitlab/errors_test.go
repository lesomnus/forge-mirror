package gitlab

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	gl "gitlab.com/gitlab-org/api/client-go"
)

// These lock in how the client reports the two statuses this package treats as
// ordinary rather than as failures.
//
// Both are the normal path, not the exception: a target that has never been
// mirrored into answers 404 for every lookup, and every run after the first
// answers 409 for every label. Reading either of them wrong turns a healthy run
// into a failed one, and it did — an earlier version matched 404 by response
// code, which this client never produces, so mirroring into an empty GitLab
// failed on the first call with a bare "404 Not Found".
//
// A client upgrade that moves either of these should fail here, not in an
// end-to-end run five minutes long.
func TestIsNotFound(t *testing.T) {
	t.Run("the client's 404 sentinel", func(t *testing.T) {
		require.True(t, isNotFound(gl.ErrNotFound))
	})
	t.Run("wrapped", func(t *testing.T) {
		require.True(t, isNotFound(fmt.Errorf("get project: %w", gl.ErrNotFound)))
	})
	t.Run("an error response is not a 404", func(t *testing.T) {
		// 404 is the one status this client does *not* report this way.
		require.False(t, isNotFound(&gl.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusNotFound},
		}))
	})
	t.Run("other errors", func(t *testing.T) {
		require.False(t, isNotFound(errors.New("boom")))
		require.False(t, isNotFound(nil))
	})
}

func TestIsConflict(t *testing.T) {
	t.Run("409", func(t *testing.T) {
		require.True(t, isConflict(&gl.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusConflict},
		}))
	})
	t.Run("wrapped", func(t *testing.T) {
		err := fmt.Errorf("create label: %w", &gl.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusConflict},
		})
		require.True(t, isConflict(err))
	})
	t.Run("another status", func(t *testing.T) {
		require.False(t, isConflict(&gl.ErrorResponse{
			Response: &http.Response{StatusCode: http.StatusBadRequest},
		}))
	})
	t.Run("the 404 sentinel is not a conflict", func(t *testing.T) {
		require.False(t, isConflict(gl.ErrNotFound))
	})
	t.Run("other errors", func(t *testing.T) {
		require.False(t, isConflict(errors.New("boom")))
		require.False(t, isConflict(nil))
	})
}
