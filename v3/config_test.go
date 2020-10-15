package circuit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeneralConfig_Merge(t *testing.T) {

	t.Run("respect ForceOpen field of args cfg", func(t *testing.T) {
		cfg := GeneralConfig{}

		cfg.merge(GeneralConfig{ForceOpen: true})

		assert.True(t, cfg.ForceOpen, "expect to be true")
	})

	t.Run("respect ForceOpen field of receiver cfg", func(t *testing.T) {
		cfg := GeneralConfig{ForceOpen: true}

		cfg.merge(GeneralConfig{ForceOpen: false})

		assert.True(t, cfg.ForceOpen, "expect to be true")
	})

	t.Run("respect ForceClosed field of args cfg", func(t *testing.T) {
		cfg := GeneralConfig{}

		cfg.merge(GeneralConfig{ForcedClosed: true})

		assert.True(t, cfg.ForcedClosed, "expect to be true")
	})

	t.Run("respect ForceClosed field of receiver cfg", func(t *testing.T) {
		cfg := GeneralConfig{ForcedClosed: true}

		cfg.merge(GeneralConfig{ForceOpen: false})

		assert.True(t, cfg.ForcedClosed, "expect to be true")
	})

}

func TestExecutionConfig_Merge(t *testing.T) {

	t.Run("isErrInterrupt check function", func(t *testing.T) {
		cfg := ExecutionConfig{}

		cfg.merge(ExecutionConfig{IsErrInterrupt: func(e error) bool { return e != nil }})

		assert.NotNil(t, cfg.IsErrInterrupt)
	})

	t.Run("ignore isErrInterrupt if previously set", func(t *testing.T) {
		fn1 := func(err error) bool { return true }
		fn2 := func(err error) bool { return false }

		cfg := ExecutionConfig{
			IsErrInterrupt: fn1,
		}

		cfg.merge(ExecutionConfig{IsErrInterrupt: fn2})

		assert.NotNil(t, fn1, cfg.IsErrInterrupt)
		assert.True(t, cfg.IsErrInterrupt(nil))
	})
}
