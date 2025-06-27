# User Agent extractor

Implementation over [github.com/ua-parser/uap-go/uaparser](https://github.com/ua-parser/uap-go) uses the regexes defined in the [core module](https://github.com/ua-parser/uap-core/blob/master/regexes.yaml) and includes a couple of regexes specifically for the terraform provider.

## build

We provided a simple build.sh file so you can compile the regexes.yaml into a go file, make sure you recompile if you change the regexes

## Custom regexes

```yml
user_agent_parsers:
  - regex: (Terraform|HashiCorp)/(\d+)\.(\d+)(?:\.(\d+))?
    family_replacement: Terraform
    v1_replacement: $2 # Major version of Terraform
    v2_replacement: $3 # Minor version of Terraform
    v3_replacement: $4 # Optional patch version of Terraform (may be empty)
device_parsers:
  - regex: terraform-provider-([a-zA-Z0-9_-]+?)/(\d+)\.(\d+)(?:\.(\d+))?
    device_replacement: $1/$2.$3.$4
    brand_replacement: $2.$3.$4
```
