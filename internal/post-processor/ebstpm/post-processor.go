//go:generate packer-sdc mapstructure-to-hcl2 -type Config

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
	"time"
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

	if len(errs.Errors) > 0 {
		return errs
	}

	return nil
}

func (p *PostProcessor) PostProcess(ctx context.Context, ui packer.Ui, artifact packer.Artifact) (a packer.Artifact, keep bool, forceOverride bool, err error) {
	session, err := p.config.Session()
	generatedData := artifact.State("generated_data")
	if generatedData == nil {
		// Make sure it's not a nil map so we can assign to it later.
		generatedData = make(map[string]interface{})
	}
	p.config.ctx.Data = generatedData

	bId := artifact.BuilderId()

	if bId != "mitchellh.amazonebs" {
		ui.Say("Skipping secureboot post-process.")
		return artifact, true, true, nil
	}

	ui.Say("Creating secureboot images...")
	amis := processArtifact(artifact.Id(), p.config, ui)

	if len(amis) > 0 {
		emptyState := make(map[string]interface{})
		newArtifact := &awscommon.Artifact{
			Amis:           amis,
			BuilderIdValue: BuilderId,
			StateData:      emptyState,
			Session:        session,
		}
		err := artifact.Destroy()
		if err != nil {
			ui.Error(fmt.Sprintf("Error: %s", err))
		}
		return newArtifact, true, false, nil
	}

	return artifact, true, true, nil
}

func processArtifact(id string, config Config, ui packer.Ui) map[string]string {
	images := make(map[string]string)
	artifacts := strings.Split(id, ",")
	for _, artifact := range artifacts {
		parts := strings.Split(artifact, ":")
		if len(parts) == 2 {
			region := parts[0]
			amiID := parts[1]
			imageId, err := createTPM(region, amiID, config, ui)
			if err != nil {
				ui.Error(fmt.Sprintf("Error creating image: %s", err))
			}
			if imageId != nil {
				images[region] = aws.StringValue(imageId)
			}
		}
	}
	return images
}

func createTPM(region string, amiID string, config Config, ui packer.Ui) (*string, error) {
	session, err := config.Session()
	if err != nil {
		return nil, err
	}

	ec2Svc := ec2.New(session, &aws.Config{
		Region: aws.String(region),
	})

	describeInput := &ec2.DescribeImagesInput{
		ImageIds: []*string{aws.String(amiID)},
	}
	describeOutput, err := ec2Svc.DescribeImages(describeInput)
	if err != nil {
		return nil, err
	}

	if len(describeOutput.Images) != 1 {
		return nil, errors.New("multiple images found, expected 1")
	}

	image := describeOutput.Images[0]

	describeAttributeInput := ec2.DescribeImageAttributeInput{
		Attribute: aws.String("launchPermission"),
		ImageId:   image.ImageId,
	}

	describeAttributeOutput, err := ec2Svc.DescribeImageAttribute(&describeAttributeInput)
	if err != nil {
		return nil, err
	}

	mappings := cloneBlockDeviceMappings(image.BlockDeviceMappings)

	input := &ec2.RegisterImageInput{
		Architecture:        image.Architecture,
		BlockDeviceMappings: mappings,
		BootMode:            aws.String("uefi"),
		Description:         image.Description,
		EnaSupport:          image.EnaSupport,
		ImageLocation:       image.ImageLocation,
		ImdsSupport:         image.ImdsSupport,
		KernelId:            image.KernelId,
		Name:                image.Name,
		RamdiskId:           image.RamdiskId,
		RootDeviceName:      image.RootDeviceName,
		SriovNetSupport:     image.SriovNetSupport,
		TpmSupport:          aws.String(config.TPMVersion),
		UefiData:            &config.UEFIData,
		VirtualizationType:  image.VirtualizationType,
	}

	if config.AMIName != "" {
		input.Name = aws.String(config.AMIName)
	}

	deregisterInput := &ec2.DeregisterImageInput{
		ImageId: image.ImageId,
	}
	_, err = ec2Svc.DeregisterImage(deregisterInput)
	if err == nil {
		ui.Say(fmt.Sprintf("Deregistered AMI %s at %s", aws.StringValue(image.ImageId), region))
	}

	result, err := ec2Svc.RegisterImage(input)
	if err != nil {
		return nil, err
	}
	imageID := aws.StringValue(result.ImageId)
	log.Printf("Registered new AMI ID: %s", imageID)

	createTagsInput := &ec2.CreateTagsInput{
		Resources: []*string{result.ImageId},
		Tags:      image.Tags,
	}
	_, err = ec2Svc.CreateTags(createTagsInput)

	if describeAttributeOutput.LaunchPermissions != nil {
		launchPermissionInput := &ec2.ModifyImageAttributeInput{
			ImageId: result.ImageId,
			LaunchPermission: &ec2.LaunchPermissionModifications{
				Add: describeAttributeOutput.LaunchPermissions,
			},
		}
		_, err = ec2Svc.ModifyImageAttribute(launchPermissionInput)
		if err != nil {
			return nil, err
		}
		log.Printf("Copied permissions from %s to %s", aws.StringValue(image.ImageId), imageID)
	}

	if image.DeprecationTime != nil {
		deprecationTime, err := time.Parse("2006-01-02T15:04:05Z07:00", aws.StringValue(image.DeprecationTime))
		if err != nil {
			return nil, err
		}
		deprecationInput := &ec2.EnableImageDeprecationInput{
			ImageId:     result.ImageId,
			DeprecateAt: &deprecationTime,
		}
		_, err = ec2Svc.EnableImageDeprecation(deprecationInput)
		if err != nil {
			return nil, err
		}
	}

	return result.ImageId, nil
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
