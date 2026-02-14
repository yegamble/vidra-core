package usecase

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeJSONValues(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		result := mergeJSONValues(nil, nil)
		assert.Nil(t, result)
	})

	t.Run("a nil returns b", func(t *testing.T) {
		result := mergeJSONValues(nil, "hello")
		assert.Equal(t, "hello", result)
	})

	t.Run("b nil returns a", func(t *testing.T) {
		result := mergeJSONValues("hello", nil)
		assert.Equal(t, "hello", result)
	})

	t.Run("equal scalars", func(t *testing.T) {
		result := mergeJSONValues("same", "same")
		assert.Equal(t, "same", result)
	})

	t.Run("different scalars become array", func(t *testing.T) {
		result := mergeJSONValues("a", "b")
		arr, ok := result.([]interface{})
		require.True(t, ok)
		assert.Len(t, arr, 2)
	})

	t.Run("map merge simple", func(t *testing.T) {
		a := map[string]interface{}{"x": 1}
		b := map[string]interface{}{"y": 2}
		result := mergeJSONValues(a, b)
		merged, ok := result.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, 1, merged["x"])
		assert.Equal(t, 2, merged["y"])
	})

	t.Run("map merge recursive conflict", func(t *testing.T) {
		a := map[string]interface{}{
			"nested": map[string]interface{}{"a": 1},
		}
		b := map[string]interface{}{
			"nested": map[string]interface{}{"b": 2},
		}
		result := mergeJSONValues(a, b)
		merged, ok := result.(map[string]interface{})
		require.True(t, ok)
		nested, ok := merged["nested"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, 1, nested["a"])
		assert.Equal(t, 2, nested["b"])
	})

	t.Run("array merge deduplicates", func(t *testing.T) {
		a := []interface{}{"x", "y"}
		b := []interface{}{"y", "z"}
		result := mergeJSONValues(a, b)
		arr, ok := result.([]interface{})
		require.True(t, ok)
		assert.Len(t, arr, 3)
	})

	t.Run("map and non-map", func(t *testing.T) {
		a := map[string]interface{}{"x": 1}
		b := "not-a-map"
		result := mergeJSONValues(a, b)
		arr, ok := result.([]interface{})
		require.True(t, ok)
		assert.Len(t, arr, 2)
	})
}

func TestCloneRawJSON(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		result := cloneRawJSON(nil)
		assert.Nil(t, result)
	})

	t.Run("empty returns nil", func(t *testing.T) {
		result := cloneRawJSON(json.RawMessage{})
		assert.Nil(t, result)
	})

	t.Run("valid input clones", func(t *testing.T) {
		original := json.RawMessage(`{"key":"value"}`)
		cloned := cloneRawJSON(original)
		assert.Equal(t, original, cloned)
		cloned[0] = 'X'
		assert.NotEqual(t, original[0], cloned[0])
	})
}

func TestMergeJSONRawMessages(t *testing.T) {
	t.Run("merges two objects", func(t *testing.T) {
		a := json.RawMessage(`{"a":1}`)
		b := json.RawMessage(`{"b":2}`)
		result, err := mergeJSONRawMessages(a, b)
		require.NoError(t, err)

		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &m))
		assert.Equal(t, float64(1), m["a"])
		assert.Equal(t, float64(2), m["b"])
	})

	t.Run("invalid a returns error", func(t *testing.T) {
		a := json.RawMessage(`invalid`)
		b := json.RawMessage(`{"b":2}`)
		_, err := mergeJSONRawMessages(a, b)
		require.Error(t, err)
	})

	t.Run("invalid b returns error", func(t *testing.T) {
		a := json.RawMessage(`{"a":1}`)
		b := json.RawMessage(`invalid`)
		_, err := mergeJSONRawMessages(a, b)
		require.Error(t, err)
	})
}

func TestMergeJSONLabels(t *testing.T) {
	t.Run("both empty", func(t *testing.T) {
		result := mergeJSONLabels(nil, nil)
		assert.Nil(t, result)
	})

	t.Run("original empty returns duplicate", func(t *testing.T) {
		dup := json.RawMessage(`{"values":["nsfw"]}`)
		result := mergeJSONLabels(nil, dup)
		assert.Equal(t, dup, result)
	})

	t.Run("duplicate empty returns original", func(t *testing.T) {
		orig := json.RawMessage(`{"values":["safe"]}`)
		result := mergeJSONLabels(orig, nil)
		assert.Equal(t, orig, result)
	})

	t.Run("merge failure returns original", func(t *testing.T) {
		orig := json.RawMessage(`{"values":["safe"]}`)
		dup := json.RawMessage(`invalid json`)
		result := mergeJSONLabels(orig, dup)
		assert.Equal(t, orig, result)
	})
}

func TestJsonValueKey(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		key := jsonValueKey("hello")
		assert.Equal(t, `"hello"`, key)
	})

	t.Run("number", func(t *testing.T) {
		key := jsonValueKey(42)
		assert.Equal(t, "42", key)
	})

	t.Run("map", func(t *testing.T) {
		key := jsonValueKey(map[string]interface{}{"a": 1})
		assert.Contains(t, key, "a")
	})
}

func TestMergeJSONArrays(t *testing.T) {
	t.Run("deduplicates values", func(t *testing.T) {
		a := []interface{}{"x", "y"}
		b := []interface{}{"y", "z"}
		result := mergeJSONArrays(a, b)
		assert.Len(t, result, 3)
	})

	t.Run("empty arrays", func(t *testing.T) {
		result := mergeJSONArrays([]interface{}{}, []interface{}{})
		assert.Empty(t, result)
	})
}
