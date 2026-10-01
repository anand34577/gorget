// Terraform provider for Gorget: manage setup keys, access policy, groups, users, webhooks and
// any settings section as code.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/anand34577/terraform-provider-gorget/internal/provider"
)

// version is set at build time.
var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "run the provider with debugger support")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/anand34577/gorget",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
