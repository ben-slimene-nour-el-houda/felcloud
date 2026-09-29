package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Config holds all verified ground truth settings for the infrastructure stack.
type Config struct {
	AvailabilityZone  string
	ImageID           string
	GatewayFlavor     string
	SandboxFlavor     string
	KeypairName       string
	ServerGroupName   string
	ExternalNetworkID string
	EdgeCIDR          string
	SandboxCIDR       string
	MgmtCIDR          string
	AdminSSHCIDR      string
}

// LoadConfig loads and validates configuration from Pulumi config.
// It fails fast, reporting all missing required keys at once.
func LoadConfig(ctx *pulumi.Context) (*Config, error) {
	c := config.New(ctx, "")

	cfg := &Config{
		AvailabilityZone:  c.Get("availabilityZone"),
		ImageID:           c.Get("imageId"),
		GatewayFlavor:     c.Get("gatewayFlavor"),
		SandboxFlavor:     c.Get("sandboxFlavor"),
		KeypairName:       c.Get("keypairName"),
		ServerGroupName:   c.Get("serverGroupName"),
		ExternalNetworkID: c.Get("externalNetworkId"),
		EdgeCIDR:          c.Get("edgeCidr"),
		SandboxCIDR:       c.Get("sandboxCidr"),
		MgmtCIDR:          c.Get("mgmtCidr"),
		AdminSSHCIDR:      c.Get("adminSshCidr"),
	}

	var missing []string
	if cfg.AvailabilityZone == "" {
		missing = append(missing, "availabilityZone")
	}
	if cfg.ImageID == "" {
		missing = append(missing, "imageId")
	}
	if cfg.GatewayFlavor == "" {
		missing = append(missing, "gatewayFlavor")
	}
	if cfg.SandboxFlavor == "" {
		missing = append(missing, "sandboxFlavor")
	}
	if cfg.KeypairName == "" {
		missing = append(missing, "keypairName")
	}
	if cfg.ServerGroupName == "" {
		missing = append(missing, "serverGroupName")
	}
	if cfg.ExternalNetworkID == "" {
		missing = append(missing, "externalNetworkId")
	}
	if cfg.EdgeCIDR == "" {
		missing = append(missing, "edgeCidr")
	}
	if cfg.SandboxCIDR == "" {
		missing = append(missing, "sandboxCidr")
	}
	if cfg.MgmtCIDR == "" {
		missing = append(missing, "mgmtCidr")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required configuration keys: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}
