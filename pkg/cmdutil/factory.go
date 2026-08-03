package cmdutil

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// Factory bundles cross-cutting dependencies. Built once in main(),
// threaded into every NewCmdXxx constructor.
type Factory struct {
	IOStreams      *iostreams.IOStreams
	ExecutableName string

	// CacheDir resolves the tool's cache directory (os.UserCacheDir() +
	// "angr-memories" -- $XDG_CACHE_HOME/angr-memories on Linux). Lazy and
	// memoized (sync.OnceValues) per byob-factory-di.1/byob-runtime-directories.2,
	// so a command that never touches the cache (site, stats) pays nothing,
	// and repeat callers within one invocation get the same resolved path.
	// Not created here -- MkdirAll happens at the first actual write.
	CacheDir func() (string, error)
}

func New() *Factory {
	return &Factory{
		IOStreams:      iostreams.System(),
		ExecutableName: "angr-memories",
		CacheDir: sync.OnceValues(func() (string, error) {
			dir, err := os.UserCacheDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(dir, "angr-memories"), nil
		}),
	}
}
