package cmdutil

import "testing"

func TestFactory_CacheDirIsLazyAndMemoized(t *testing.T) {
	calls := 0
	f := &Factory{
		CacheDir: func() (string, error) {
			calls++
			return "/tmp/whatever", nil
		},
	}
	if calls != 0 {
		t.Fatalf("CacheDir must not be called before first use, got %d calls", calls)
	}

	got1, err := f.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	got2, err := f.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if got1 != got2 {
		t.Errorf("CacheDir returned different values across calls: %q vs %q", got1, got2)
	}
}

func TestNew_CacheDirResolves(t *testing.T) {
	f := New()
	dir, err := f.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if dir == "" {
		t.Fatal("expected a non-empty resolved cache dir")
	}
}
