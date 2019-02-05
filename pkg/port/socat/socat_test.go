package socat

import (
	"testing"

	"github.com/LarsFronius/rootlesskit/pkg/port"
	"github.com/LarsFronius/rootlesskit/pkg/port/testsuite"
)

func TestSocat(t *testing.T) {
	df := func() port.ParentDriver {
		d, err := New(testsuite.TLogWriter(t, "socat.Driver"))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	testsuite.Run(t, df)
}
