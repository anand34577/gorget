package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// stringList converts a Go slice to a Terraform list of strings (never null).
func stringList(in []string) types.List {
	vals := make([]attr.Value, 0, len(in))
	for _, s := range in {
		vals = append(vals, types.StringValue(s))
	}
	l, _ := types.ListValue(types.StringType, vals)
	return l
}

// listStrings converts a Terraform list of strings to a Go slice (never nil).
func listStrings(l types.List) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	for _, v := range l.Elements() {
		if s, ok := v.(types.String); ok {
			out = append(out, s.ValueString())
		}
	}
	return out
}

// stringSet converts a slice to a Terraform set of strings.
func stringSet(in []string) types.Set {
	vals := make([]attr.Value, 0, len(in))
	for _, s := range in {
		vals = append(vals, types.StringValue(s))
	}
	s, _ := types.SetValue(types.StringType, vals)
	return s
}

// setStrings converts a Terraform set of strings to a Go slice (never nil).
func setStrings(s types.Set) []string {
	out := []string{}
	if s.IsNull() || s.IsUnknown() {
		return out
	}
	for _, v := range s.Elements() {
		if str, ok := v.(types.String); ok {
			out = append(out, str.ValueString())
		}
	}
	return out
}
