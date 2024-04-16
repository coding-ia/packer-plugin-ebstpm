package ebstpm

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/hcl/v2/hcldec"
	awscommon "github.com/hashicorp/packer-plugin-amazon/builder/common"
	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	"log"
	"strings"
)

const BuilderId = "packer.post-processor.ebstpm-secureboot"

type Config struct {
	common.PackerConfig    `mapstructure:",squash"`
	awscommon.AccessConfig `mapstructure:",squash"`

	AMIName    string `mapstructure:"ami_name"`
	TPMVersion string `mapstructure:"tpm_version"`
	UEFIData   string `mapstructure:"uefi_data"`

	ctx interpolate.Context
}

type PostProcessor struct {
	config Config
}

func (p *PostProcessor) ConfigSpec() hcldec.ObjectSpec {
	return p.config.FlatMapstructure().HCL2Spec()
}

func (p *PostProcessor) Configure(raws ...interface{}) error {
	//p.config.ctx.Funcs = awscommon.TemplateFuncs
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

	errs := new(packer.MultiError)

	if p.config.TPMVersion == "" {
		p.config.TPMVersion = "v2.0"
	}

	if p.config.UEFIData == "" {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("uefi_data is not set"))
	}

	if p.config.AMIName == "" {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("ami_name is not set"))
	}

	if len(errs.Errors) > 0 {
		return errs
	}

	return nil
}

func (p *PostProcessor) PostProcess(ctx context.Context, ui packer.Ui, artifact packer.Artifact) (a packer.Artifact, keep bool, forceOverride bool, err error) {
	generatedData := artifact.State("generated_data")
	if generatedData == nil {
		// Make sure it's not a nil map so we can assign to it later.
		generatedData = make(map[string]interface{})
	}
	p.config.ctx.Data = generatedData

	bId := artifact.BuilderId()
	_ = bId

	err = processArtifact(artifact.Id(), p.config)
	if err != nil {
		return artifact, true, true, err
	}

	return artifact, true, true, nil
}

func processArtifact(id string, config Config) error {
	parts := strings.Split(id, ":")
	if len(parts) == 2 {
		region := parts[0]
		amiID := parts[1]
		err := createTPM(region, amiID, config)
		if err != nil {
			return err
		}
	}
	return nil
}

func createTPM(region string, amiID string, config Config) error {
	session, err := config.Session()
	if err != nil {
		return err
	}

	ec2Svc := ec2.New(session, &aws.Config{
		Region: aws.String(region),
	})

	describeInput := &ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String(amiID)},
	}
	describeOutput, err := ec2Svc.DescribeImages(describeInput)
	if err != nil {
		return err
	}

	if len(describeOutput.Images) != 1 {
		return errors.New("multiple images found, expected 1")
	}

	image := describeOutput.Images[0]

	describeAttributeInput := ec2.DescribeImageAttributeInput{
		Attribute: aws.String("launchPermission"),
		ImageId:   image.ImageId,
	}

	describeAttributeOutput, err := ec2Svc.DescribeImageAttribute(&describeAttributeInput)
	if err != nil {
		return err
	}
	_ = describeAttributeOutput

	mappings := cloneBlockDeviceMappings(image.BlockDeviceMappings)

	input := &ec2.RegisterImageInput{
		Architecture:        image.Architecture,
		BlockDeviceMappings: mappings,
		BootMode:            aws.String("uefi"),
		Description:         image.Description,
		EnaSupport:          image.EnaSupport,
		ImdsSupport:         image.ImdsSupport,
		Name:                aws.String(config.AMIName),
		RootDeviceName:      image.RootDeviceName,
		TpmSupport:          aws.String(config.TPMVersion),
		UefiData:            &config.UEFIData,
	}

	result, err := ec2Svc.RegisterImage(input)
	if err != nil {
		return err
	}
	imageID := aws.StringValue(result.ImageId)
	log.Printf("Registered new AMI ID: %s", imageID)

	if describeAttributeOutput.LaunchPermissions != nil {
		launchPermissionInput := &ec2.ModifyImageAttributeInput{
			ImageId: result.ImageId,
			LaunchPermission: &ec2.LaunchPermissionModifications{
				Add: describeAttributeOutput.LaunchPermissions,
			},
		}
		_, err = ec2Svc.ModifyImageAttribute(launchPermissionInput)
		if err != nil {
			return err
		}
		log.Printf("Copied permissions from %s to %s", aws.StringValue(image.ImageId), imageID)
	}

	return nil
}

func cloneBlockDeviceMappings(blockMappings []*ec2.BlockDeviceMapping) []*ec2.BlockDeviceMapping {
	var mappings []*ec2.BlockDeviceMapping
	for i := 0; i < len(blockMappings); i++ {
		if blockMappings[i].Ebs != nil {
			mapping := &ec2.BlockDeviceMapping{
				DeviceName: blockMappings[i].DeviceName,
				Ebs: &ec2.EbsBlockDevice{
					SnapshotId: blockMappings[i].Ebs.SnapshotId,
				},
			}
			mappings = append(mappings, mapping)
		}
	}
	return mappings
}
