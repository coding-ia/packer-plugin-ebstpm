package ebstpm

import (
	"context"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

const BuilderId = "packer.post-processor.create-secureboot"

type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	ctx interpolate.Context
}

type PostProcessor struct {
	config Config
}

func (p PostProcessor) ConfigSpec() hcldec.ObjectSpec {
	return p.config.FlatMapstructure().HCL2Spec()
}

func (p PostProcessor) Configure(raws ...interface{}) error {
	err := config.Decode(&p.config, &config.DecodeOpts{
		PluginType:         BuilderId,
		Interpolate:        true,
		InterpolateContext: &p.config.ctx,
		InterpolateFilter: &interpolate.RenderFilter{
			Exclude: []string{},
		},
	}, raws...)
	if err != nil {
		return err
	}

	return nil
}

func (p PostProcessor) PostProcess(ctx context.Context, ui packer.Ui, artifact packer.Artifact) (a packer.Artifact, keep bool, forceOverride bool, err error) {
	return artifact, true, true, nil
}
