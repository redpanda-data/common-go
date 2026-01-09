package {{ .Pkg }}

{{ .TODOComment }}

import (
	"testing"

  "github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/redpanda-data/common-go/rp-controller-utils/deprecations"
)

func TestDeprecatedFieldWarnings(t *testing.T) {
	tests := []struct {
		name string
		obj client.Object
		wantWarnings []string
	}{
	{{- range .Objs }}
		{
			name: "{{ .Name }}",
			obj: {{ .Literal }},
			wantWarnings: []string{
		{{- range .Warnings }}
				"{{ . }}",
		{{- end }}
			},
		},
	{{- end }}
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ElementsMatch(t, tc.wantWarnings, deprecations.FindDeprecatedFieldWarnings(tc.obj))
		})
	}
}