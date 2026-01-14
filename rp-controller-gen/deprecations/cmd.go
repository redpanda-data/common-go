// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deprecations

import (
	_ "embed"
	"errors"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

// DeprecationPrefix is the field name prefix used to identify deprecated
// fields when scanning Go structs for deprecations.
const DeprecationPrefix = "Deprecated"

var (
	testGenerator *template.Template
	//go:embed templates/test.tpl
	testTemplate string
)

func init() {
	helpers := map[string]any{
		"year": func() string {
			return time.Now().Format("2006")
		},
	}
	testGenerator = template.Must(template.New("tests").Funcs(helpers).Parse(testTemplate))
}

// DeprecationConfig configures how deprecation tests are discovered and
// generated.
type DeprecationConfig struct {
	Directory   string
	PackageName string
	OutputFile  string
	Verbose     bool
}

type fieldRef struct {
	GoPath   []string // Go field names e.g. [Spec ClusterSpec DeprecatedFullNameOverride]
	JsonPath []string // JSON names e.g. [spec clusterSpec fullNameOverride]
	TypeExpr ast.Expr
}

type objSpec struct {
	Name     string
	Literal  string
	Warnings []string
}

// Debugf emits debug output when the config's Verbose flag is set.
func (c DeprecationConfig) Debugf(format string, a ...any) {
	if !c.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, format, a...)
}

// Cmd returns a cobra command that generates tests for deprecated API
// fields based on the provided directory and package configuration.
func Cmd() *cobra.Command {
	var config DeprecationConfig

	cmd := &cobra.Command{
		Use:     "deprecations",
		Short:   "Generate tests for deprecated API fields",
		Example: "gen deprecations --directory ./api/example/v1alpha2",
		RunE: func(*cobra.Command, []string) error {
			return Render(config)
		},
	}

	cmd.Flags().StringVar(&config.Directory, "directory", ".", "The directory to scan for deprecated fields")
	cmd.Flags().StringVar(&config.PackageName, "package", "", "The name of the package, if not specified we try and figure it out dynamically")
	cmd.Flags().StringVar(&config.OutputFile, "output-file", "zz_generated.deprecations_test.go", "The name of the file to output in the given directory")
	cmd.Flags().BoolVarP(&config.Verbose, "verbose", "v", false, "Enable debug output.")

	return cmd
}

// Render scans the configured directory for types with deprecated fields and
// writes generated test code to the configured OutputFile.
func Render(config DeprecationConfig) error {
	dir := config.Directory

	if config.PackageName == "" {
		config.PackageName = strings.Trim(filepath.Base(dir), ".")
		config.PackageName = strings.Trim(filepath.Base(dir), "/")
	}

	if config.PackageName == "" {
		return errors.New("could not determine package name")
	}

	parser := NewParser(config)

	if err := parser.Parse(); err != nil {
		return err
	}

	contents, err := parser.Compile()
	if err != nil {
		return err
	}

	outPath := filepath.Join(dir, config.OutputFile)
	if err := os.WriteFile(outPath, contents, 0o644); err != nil { //nolint:gosec // file permissions are correct
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("wrote %s\n", outPath)

	return nil
}
