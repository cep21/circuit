package hystrix

import (
	"testing"
	"time"
)

func TestFactory(t *testing.T) {
	f := Factory{
		CreateConfigureCloser: []func(circuitName string) ConfigureCloser{
			func(_ string) ConfigureCloser {
				return ConfigureCloser{
					SleepWindow: time.Second,
				}
			},
		},
		CreateConfigureOpener: []func(circuitName string) ConfigureOpener{
			func(_ string) ConfigureOpener {
				return ConfigureOpener{
					RequestVolumeThreshold: 10,
				}
			},
		},
	}
	cfg := f.Configure("testing")
	x := cfg.General.OpenToClosedFactory().(*Closer)
	if x.config.SleepWindow != time.Second {
		t.Fatal("Expected a second sleep window")
	}

	y := cfg.General.ClosedToOpenFactory().(*Opener)
	if y.config.RequestVolumeThreshold != 10 {
		t.Fatal("Expected 10 request volume threshold")
	}
}

func TestConfigureOpener(t *testing.T) {
	now := time.Now()
	c := ConfigureOpener{
		RequestVolumeThreshold: 10,
		Now: func() time.Time {
			return now
		},
	}
	if !c.now().Equal(now) {
		t.Fatal("now not using set Now")
	}
}
