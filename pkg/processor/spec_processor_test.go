package processor

import (
	"strconv"
	"testing"

	"github.com/atlassian/smith"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSpecProcessor(t *testing.T) {
	t.Parallel()
	sp := SpecProcessor{
		selfName:       "abc",
		readyResources: readyResources(),
		allowedResources: map[smith.ResourceName]struct{}{
			"res1": {},
		},
	}
	obj := map[string]interface{}{
		"ref": map[string]interface{}{
			"string":     "str>$(res1/a/string)<str",
			"intStr":     "str>$(res1/a/int)<str",
			"boolStr":    "str>$(res1/a/bool)<str",
			"float64Str": "str>$(res1/a/float64)<str",

			"int":     "$((res1/a/int))",
			"bool":    "$((res1/a/bool))",
			"float64": "$((res1/a/float64))",
			"object":  "$((res1/a/object))",
		},
	}
	expected := map[string]interface{}{
		"ref": map[string]interface{}{
			"string":     "str>string1<str",
			"intStr":     "str>42<str",
			"boolStr":    "str>true<str",
			"float64Str": "str>1.1<str",

			"int":     42,
			"bool":    true,
			"float64": float64(1.1),
			"object": map[string]interface{}{
				"a": 1,
				"b": "str",
			},
		},
	}

	require.NoError(t, sp.ProcessObject(obj))
	assert.Equal(t, expected, obj)
}

func TestSpecProcessorErrors(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		obj map[string]interface{}
		err string
	}{
		{
			obj: map[string]interface{}{
				"invalid": "$((res1/something))",
			},
			err: `invalid reference at "invalid": field not found: res1/something`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((res1/a/string))",
			},
			err: `invalid reference at "invalid": cannot expand field res1/a/string of type string as naked reference`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((res1/a/string))b",
			},
			err: `invalid reference at "invalid": naked reference in the middle of a string`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "a$((res1/a/string))",
			},
			err: `invalid reference at "invalid": naked reference in the middle of a string`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((res2/a/string))",
			},
			err: `invalid reference at "invalid": object not found: res2/a/string`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((res1))",
			},
			err: `invalid reference at "invalid": cannot include whole object: res1`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$(res1)",
			},
			err: `invalid reference at "invalid": cannot include whole object: res1`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$(self1/x/b)",
			},
			err: `invalid reference at "invalid": self references are not allowed: self1/x/b`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((self1/x/b))",
			},
			err: `invalid reference at "invalid": self references are not allowed: self1/x/b`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "$((resX/a/string))",
			},
			err: `invalid reference at "invalid": references can only point at direct dependencies: resX/a/string`,
		},
		{
			obj: map[string]interface{}{
				"invalid": "a$(resX/a/string)b",
			},
			err: `invalid reference at "invalid": references can only point at direct dependencies: resX/a/string`,
		},
	}
	for i, input := range inputs {
		input := input
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			sp := SpecProcessor{
				selfName:       "self1",
				readyResources: readyResources(),
				allowedResources: map[smith.ResourceName]struct{}{
					"res1": {},
				},
			}
			assert.EqualError(t, sp.ProcessObject(input.obj), input.err)
		})
	}
}

func readyResources() map[smith.ResourceName]*unstructured.Unstructured {
	return map[smith.ResourceName]*unstructured.Unstructured{
		"res1": {
			Object: map[string]interface{}{
				"a": map[string]interface{}{
					"string":  "string1",
					"int":     int(42),
					"bool":    true,
					"float64": float64(1.1),
					"object": map[string]interface{}{
						"a": 1,
						"b": "str",
					},
				},
			},
		},
		"resX": {
			Object: map[string]interface{}{
				"a": map[string]interface{}{
					"string": "string1",
				},
			},
		},
	}
}
