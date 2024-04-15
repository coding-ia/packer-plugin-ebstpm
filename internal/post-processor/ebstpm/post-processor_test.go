package ebstpm

import (
	"bytes"
	"context"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
	"testing"
)

func testUi() *packersdk.BasicUi {
	return &packersdk.BasicUi{
		Reader: new(bytes.Buffer),
		Writer: new(bytes.Buffer),
	}
}

func TestPostProcessor_PostProcess(t *testing.T) {
	p := &PostProcessor{}
	artifact := &packersdk.MockArtifact{
		//BuilderIdValue: dockerimport.BuilderId,
		IdValue: "localhost:5000/foo/bar",
	}

	result, keep, forceOverride, err := p.PostProcess(context.Background(), testUi(), artifact)

	_ = result
	_ = keep
	_ = forceOverride
	_ = err
}
