package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	moduleName    = "armconsumption"
	moduleVersion = "v1.0.0"
)

// At the moment the consumption pricesheet download API is not a)
// documented or b) supported by the SDK. This is an implementation of
// a client in the style of the Azure go SDK - once the API is
// supported this will be removed.

// PriceSheetClient contains the methods for the PriceSheet group.
// Don't use this type directly, use NewPriceSheetClient() instead.
type PriceSheetClient struct {
	host             string
	billingAccountID string
	pl               runtime.Pipeline
}

// NewPriceSheetClient creates a new instance of PriceSheetClient with the specified values.
// billingAccountId - Azure Billing Account ID.
// credential - used to authorize requests. Usually a credential from azidentity.
// options - pass nil to accept the default values.
func NewPriceSheetClient(billingAccountID string, credential azcore.TokenCredential, options *arm.ClientOptions) (*PriceSheetClient, error) {
	if options == nil {
		options = &arm.ClientOptions{}
	}
	ep := cloud.AzurePublic.Services[cloud.ResourceManager].Endpoint
	if c, ok := options.Cloud.Services[cloud.ResourceManager]; ok {
		ep = c.Endpoint
	}
	pl, err := armruntime.NewPipeline(moduleName, moduleVersion, credential, runtime.PipelineOptions{}, options)
	if err != nil {
		return nil, err
	}
	client := &PriceSheetClient{
		billingAccountID: billingAccountID,
		host:             ep,
		pl:               pl,
	}
	return client, nil
}

// BeginDownloadByBillingPeriod - requests a pricesheet for a specific billing period `yyyymm`.
// Returns a Poller that will provide the download URL when the pricesheet is ready.
// If the operation fails it returns an *azcore.ResponseError type.
// Generated from API version 2022-06-01
// billingPeriodName - Billing Period Name `yyyymm`.
func (client *PriceSheetClient) BeginDownloadByBillingPeriod(ctx context.Context, billingPeriodName string) (*runtime.Poller[PriceSheetClientDownloadResponse], error) {
	resp, err := client.downloadByBillingPeriodOperation(ctx, billingPeriodName)
	if err != nil {
		return nil, err
	}
	return runtime.NewPoller[PriceSheetClientDownloadResponse](resp, client.pl, nil)
}

type PriceSheetClientDownloadResponse struct {
	ID         string                             `json:"id"`
	Name       string                             `json:"name"`
	StartTime  time.Time                          `json:"startTime"`
	EndTime    time.Time                          `json:"endTime"`
	Status     string                             `json:"status"`
	Properties PriceSheetClientDownloadProperties `json:"properties"`
}

type PriceSheetClientDownloadProperties struct {
	DownloadURL string `json:"downloadUrl"`
	ValidTill   string `json:"validTill"`
}

func (client *PriceSheetClient) downloadByBillingPeriodOperation(ctx context.Context, billingPeriodName string) (*http.Response, error) {
	req, err := client.downloadByBillingPeriodCreateRequest(ctx, billingPeriodName)
	if err != nil {
		return nil, err
	}
	resp, err := client.pl.Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusAccepted) {
		return nil, runtime.NewResponseError(resp)
	}
	return resp, nil
}

const downloadByBillingPeriodTemplate = "/providers/Microsoft.Billing/billingAccounts/%s/billingPeriods/%s/providers/Microsoft.Consumption/pricesheets/download"

// downloadByBillingPeriodCreateRequest creates the DownloadByBillingPeriod request.
func (client *PriceSheetClient) downloadByBillingPeriodCreateRequest(ctx context.Context, billingPeriodName string) (*policy.Request, error) {
	if client.billingAccountID == "" {
		return nil, errors.New("parameter client.billingAccountID cannot be empty")
	}
	if billingPeriodName == "" {
		return nil, errors.New("parameter billingPeriodName cannot be empty")
	}
	urlPath := fmt.Sprintf(downloadByBillingPeriodTemplate, url.PathEscape(client.billingAccountID), url.PathEscape(billingPeriodName))
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(client.host, urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2022-06-01")
	reqQP.Set("ln", "en")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"*/*"}
	return req, nil
}

// billingPeriodsListTemplate is the URL template for listing billing periods.
const billingPeriodsListTemplate = "/providers/Microsoft.Billing/billingAccounts/%s/billingPeriods"

// billingPeriodsListResponse represents the response from the billing periods list API.
type billingPeriodsListResponse struct {
	Value []billingPeriodEntry `json:"value"`
}

// billingPeriodEntry represents a single billing period entry.
type billingPeriodEntry struct {
	Name string `json:"name"`
}

// GetCurrentBillingPeriod fetches the most recent billing period name from the
// Azure Billing Periods List API. This handles both EA accounts (which use
// "yyyyMM" format) and MCA accounts (which use "yyyyMM-1" format).
// See: https://learn.microsoft.com/en-us/rest/api/billing/billing-periods/list
func (client *PriceSheetClient) GetCurrentBillingPeriod(ctx context.Context) (string, error) {
	if client.billingAccountID == "" {
		return "", errors.New("parameter client.billingAccountID cannot be empty")
	}
	urlPath := fmt.Sprintf(billingPeriodsListTemplate, url.PathEscape(client.billingAccountID))
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.host, urlPath))
	if err != nil {
		return "", fmt.Errorf("creating billing periods list request: %w", err)
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2020-05-01")
	// The API returns billing periods in descending order by default, so $top=1 gives the most recent period.
	reqQP.Set("$top", "1")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}

	resp, err := client.pl.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing billing periods list request: %w", err)
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return "", runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading billing periods response body: %w", err)
	}

	var result billingPeriodsListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing billing periods response: %w", err)
	}

	if len(result.Value) == 0 {
		return "", errors.New("no billing periods returned from API")
	}

	return result.Value[0].Name, nil
}
