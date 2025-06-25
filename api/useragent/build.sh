#!/usr/bin/env bash

# this script is used to build the yaml.go file from regexes.yml this is here for reference and it's basically taken from the
# apps/public-api-go/internal/useragent package this does not need to be run periodically only if the regexes.yml file is updated

# Strip out empty lines and comments for conciseness:
yaml=$(cat ./regexes.yml | sed '/\s*#/d' | sed '/^\s*$/d')

# Build and format a Go file including our sources:
echo "package useragent
var DefinitionYaml = []byte(\`$yaml\`)" | gofmt >./yaml.go
