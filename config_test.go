package circuit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeneralConfig_Merge(t *testing.T) {

	t.Run("respect Disabled field of args cfg", func(t *testing.T) {
		cfg := GeneralConfig{}

		cfg.merge(GeneralConfig{Disabled: true})

		assert.True(t, cfg.Disabled, "expect to be true")
	})

	t.Run("respect Disabled field of receiver cfg", func(t *testing.T) {
		cfg := GeneralConfig{Disabled: true}

		cfg.merge(GeneralConfig{Disabled: false})

		assert.True(t, cfg.Disabled, "expect to be true")
	})

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

func TestGeneralConfig_MergeCustomConfig(t *testing.T) {
	t.Run("nothing to merge leaves nil map", func(t *testing.T) {
		cfg := GeneralConfig{}
		cfg.merge(GeneralConfig{})
		assert.Nil(t, cfg.CustomConfig)
	})

	t.Run("copies into nil receiver map without aliasing", func(t *testing.T) {
		other := GeneralConfig{CustomConfig: map[interface{}]interface{}{"a": 1, "b": "two"}}
		cfg := GeneralConfig{}
		cfg.merge(other)
		assert.Equal(t, map[interface{}]interface{}{"a": 1, "b": "two"}, cfg.CustomConfig)
		cfg.CustomConfig["c"] = 3
		_, leaked := other.CustomConfig["c"]
		assert.False(t, leaked, "merge must copy, not alias, the other map")
	})

	t.Run("receiver keys win", func(t *testing.T) {
		cfg := GeneralConfig{CustomConfig: map[interface{}]interface{}{"a": "mine"}}
		cfg.merge(GeneralConfig{CustomConfig: map[interface{}]interface{}{"a": "other-a", "b": "other-b"}})
		assert.Equal(t, map[interface{}]interface{}{"a": "mine", "b": "other-b"}, cfg.CustomConfig)
	})

	t.Run("Config.Merge carries CustomConfig", func(t *testing.T) {
		cfg := Config{}
		cfg.Merge(Config{General: GeneralConfig{CustomConfig: map[interface{}]interface{}{"k": "v"}}})
		assert.Equal(t, "v", cfg.General.CustomConfig["k"])
	})
}
