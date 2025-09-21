// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package workmail

import (
	"context"
	"errors"
	"time"

	"github.com/YakDriver/smarterr"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workmail"
	awstypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	sdkid "github.com/hashicorp/terraform-plugin-sdk/v2/helper/id"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/fwdiag"
	"github.com/hashicorp/terraform-provider-aws/internal/framework"
	"github.com/hashicorp/terraform-provider-aws/internal/framework/flex"
	fwtypes "github.com/hashicorp/terraform-provider-aws/internal/framework/types"
	"github.com/hashicorp/terraform-provider-aws/internal/smerr"
	"github.com/hashicorp/terraform-provider-aws/internal/sweep"
	sweepfw "github.com/hashicorp/terraform-provider-aws/internal/sweep/framework"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// TIP: ==== FILE STRUCTURE ====
// 1. Package declaration
// 2. Imports
// 3. Main resource struct with schema method
// 4. Create, read, update, delete methods (in that order)
// 5. Other functions (flatteners, expanders, waiters, finders, etc.)

// @FrameworkResource("aws_workmail_organization", name="Organization")
func newResourceOrganization(_ context.Context) (resource.ResourceWithConfigure, error) {
	r := &resourceOrganization{}

	r.SetDefaultCreateTimeout(10 * time.Minute)
	r.SetDefaultUpdateTimeout(10 * time.Minute)
	r.SetDefaultDeleteTimeout(10 * time.Minute)

	return r, nil
}

const (
	ResNameOrganization = "Organization"
)

type resourceOrganization struct {
	framework.ResourceWithModel[resourceOrganizationModel]
	framework.WithTimeouts
}

// Schema leaving out directory_id, kms_key_arn, domains, and enable_interoperability until later
func (r *resourceOrganization) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			names.AttrARN: framework.ARNAttributeComputedOnly(),
			names.AttrDescription: schema.StringAttribute{
				Optional: true,
			},
			names.AttrID: framework.IDAttribute(), // OrganizationId
			names.AttrAlias: schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			names.AttrTimeouts: timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
			}),
		},
	}
}

func (r *resourceOrganization) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// TIP: ==== RESOURCE CREATE ====
	// Generally, the Create function should do the following things. Make
	// sure there is a good reason if you don't do one of these.
	//
	// 3. Populate a create input structure
	// 5. Using the output from the create function, set the minimum arguments
	//    and attributes for the Read function to work, as well as any computed
	//    only attributes.

	conn := r.Meta().WorkMailClient(ctx)

	var plan resourceOrganizationModel
	smerr.EnrichAppend(ctx, &resp.Diagnostics, req.Plan.Get(ctx, &plan))
	if resp.Diagnostics.HasError() {
		return
	}

	var input workmail.CreateOrganizationInput
	smerr.EnrichAppend(ctx, &resp.Diagnostics, flex.Expand(ctx, plan, &input))
	if resp.Diagnostics.HasError() {
		return
	}
	// Additional fields.
	input.ClientToken = aws.String(sdkid.UniqueId())

	out, err := conn.CreateOrganization(ctx, &input)
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, plan.Alias.String())
		return
	}
	if out == nil || out.OrganizationId == nil {
		smerr.AddError(ctx, &resp.Diagnostics, errors.New("empty output"), smerr.ID, plan.Alias.String())
		return
	}

	smerr.EnrichAppend(ctx, &resp.Diagnostics, flex.Flatten(ctx, out, &plan))
	if resp.Diagnostics.HasError() {
		return
	}

	createTimeout := r.CreateTimeout(ctx, plan.Timeouts)
	_, err = waitOrganizationCreated(ctx, conn, plan.ID.ValueString(), createTimeout)
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, plan.Alias.String())
		return
	}

	smerr.EnrichAppend(ctx, &resp.Diagnostics, resp.State.Set(ctx, plan))
}

func (r *resourceOrganization) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// 5. Set the arguments and attributes

	conn := r.Meta().WorkMailClient(ctx)

	var state resourceOrganizationModel
	smerr.EnrichAppend(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := findOrganizationByID(ctx, conn, state.ID.ValueString())
	if tfresource.NotFound(err) {
		resp.Diagnostics.Append(fwdiag.NewResourceNotFoundWarningDiagnostic(err))
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, state.ID.String())
		return
	}

	// TIP: -- 5. Set the arguments and attributes
	smerr.EnrichAppend(ctx, &resp.Diagnostics, flex.Flatten(ctx, out, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	smerr.EnrichAppend(ctx, &resp.Diagnostics, resp.State.Set(ctx, &state))
}

func (r *resourceOrganization) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	conn := r.Meta().WorkMailClient(ctx)

	var state resourceOrganizationModel
	smerr.EnrichAppend(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	input := workmail.DeleteOrganizationInput{
		OrganizationId: state.ID.ValueStringPointer(),
		ClientToken:    aws.String(sdkid.UniqueId()),
	}

	_, err := conn.DeleteOrganization(ctx, &input)
	if err != nil {
		if errs.IsA[*awstypes.ResourceNotFoundException](err) {
			return
		}

		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, state.ID.String())
		return
	}

	deleteTimeout := r.DeleteTimeout(ctx, state.Timeouts)
	_, err = waitOrganizationDeleted(ctx, conn, state.ID.ValueString(), deleteTimeout)
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, state.ID.String())
		return
	}
}

func (r *resourceOrganization) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root(names.AttrID), req, resp)
}

const (
	// TODO need to verify these are connect, i wish these where in awstypes...
	// could be "Requested"
	statusCreating = "Creating"
	statusDeleting = "Deleting"
	statusActive   = "Active"
	statusDeleted  = "Deleted"
)

func waitOrganizationCreated(ctx context.Context, conn *workmail.Client, id string, timeout time.Duration) (*awstypes.Organization, error) {
	stateConf := &retry.StateChangeConf{
		Pending:                   []string{statusCreating},
		Target:                    []string{statusActive},
		Refresh:                   statusOrganization(ctx, conn, id),
		Timeout:                   timeout,
		NotFoundChecks:            20,
		ContinuousTargetOccurence: 2,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)
	if out, ok := outputRaw.(*workmail.DescribeOrganizationOutput); ok {
		return out, smarterr.NewError(err)
	}

	return nil, smarterr.NewError(err)
}

func waitOrganizationDeleted(ctx context.Context, conn *workmail.Client, id string, timeout time.Duration) (*awstypes.Organization, error) {
	stateConf := &retry.StateChangeConf{
		Pending: []string{statusDeleting, statusActive},
		Target:  []string{statusDeleted},
		Refresh: statusOrganization(ctx, conn, id),
		Timeout: timeout,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)
	if out, ok := outputRaw.(*workmail.Organization); ok {
		return out, smarterr.NewError(err)
	}

	return nil, smarterr.NewError(err)
}

// TODO need to verify out.State responses.
func statusOrganization(ctx context.Context, conn *workmail.Client, id string) retry.StateRefreshFunc {
	return func() (any, string, error) {
		out, err := findOrganizationByID(ctx, conn, id)
		if tfresource.NotFound(err) {
			return nil, "", nil
		}

		if err != nil {
			return nil, "", smarterr.NewError(err)
		}

		return out, aws.ToString(out.State), nil
	}
}

func findOrganizationByID(ctx context.Context, conn *workmail.Client, id string) (*workmail.DescribeOrganizationOutput, error) {
	input := workmail.DescribeOrganizationInput{
		OrganizationId: aws.String(id),
	}

	out, err := conn.DescribeOrganization(ctx, &input)
	if err != nil {
		if errs.IsA[*awstypes.ResourceNotFoundException](err) {
			return nil, smarterr.NewError(&retry.NotFoundError{
				LastError:   err,
				LastRequest: &input,
			})
		}

		return nil, smarterr.NewError(err)
	}

	if out == nil || out.OrganizationId == nil {
		return nil, smarterr.NewError(tfresource.NewEmptyResultError(&input))
	}

	return out, nil
}

// See more:
// https://developer.hashicorp.com/terraform/plugin/framework/handling-data/accessing-values
type resourceOrganizationModel struct {
	framework.WithRegionModel
	ARN         types.String   `tfsdk:"arn"`
	Description types.String   `tfsdk:"description"`
	ID          types.String   `tfsdk:"id"` // TODO might be organisationId
	Alias       types.String   `tfsdk:"alias"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func sweepOrganizations(ctx context.Context, client *conns.AWSClient) ([]sweep.Sweepable, error) {
	input := workmail.ListOrganizationsInput{}
	conn := client.WorkMailClient(ctx)
	var sweepResources []sweep.Sweepable

	pages := workmail.NewListOrganizationsPaginator(conn, &input)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, smarterr.NewError(err)
		}

		for _, v := range page.OrganizationSummaries {
			sweepResources = append(sweepResources, sweepfw.NewSweepResource(newResourceOrganization, client,
				sweepfw.NewAttribute(names.AttrID, aws.ToString(v.OrganizationId))),
			)
		}
	}

	return sweepResources, nil
}
