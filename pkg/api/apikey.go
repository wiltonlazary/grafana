package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/grafana/grafana/pkg/api/dtos"
	"github.com/grafana/grafana/pkg/api/response"
	"github.com/grafana/grafana/pkg/components/apikeygen"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/web"
)

// swagger:route GET /auth/keys api_keys getAPIkeys
//
// Get auth keys.
//
// Will return auth keys.
//
// Responses:
// 200: getAPIkeyResponse
// 401: unauthorisedError
// 403: forbiddenError
// 404: notFoundError
// 500: internalServerError
func (hs *HTTPServer) GetAPIKeys(c *models.ReqContext) response.Response {
	query := models.GetApiKeysQuery{OrgId: c.OrgId, User: c.SignedInUser, IncludeExpired: c.QueryBool("includeExpired")}

	if err := hs.SQLStore.GetAPIKeys(c.Req.Context(), &query); err != nil {
		return response.Error(500, "Failed to list api keys", err)
	}

	ids := map[string]bool{}
	result := make([]*dtos.ApiKeyDTO, len(query.Result))
	for i, t := range query.Result {
		ids[strconv.FormatInt(t.Id, 10)] = true
		var expiration *time.Time = nil
		if t.Expires != nil {
			v := time.Unix(*t.Expires, 0)
			expiration = &v
		}
		result[i] = &dtos.ApiKeyDTO{
			Id:         t.Id,
			Name:       t.Name,
			Role:       t.Role,
			Expiration: expiration,
		}
	}

	metadata := hs.getMultiAccessControlMetadata(c, c.OrgId, "apikeys:id", ids)
	if len(metadata) > 0 {
		for _, key := range result {
			key.AccessControl = metadata[strconv.FormatInt(key.Id, 10)]
		}
	}

	return response.JSON(http.StatusOK, result)
}

// swagger:route DELETE /auth/keys/{id} api_keys deleteAPIkey
//
// Delete API key.
//
// Responses:
// 200: okResponse
// 401: unauthorisedError
// 403: forbiddenError
// 404: notFoundError
// 500: internalServerError
func (hs *HTTPServer) DeleteAPIKey(c *models.ReqContext) response.Response {
	id, err := strconv.ParseInt(web.Params(c.Req)[":id"], 10, 64)
	if err != nil {
		return response.Error(http.StatusBadRequest, "id is invalid", err)
	}

	cmd := &models.DeleteApiKeyCommand{Id: id, OrgId: c.OrgId}
	err = hs.SQLStore.DeleteApiKey(c.Req.Context(), cmd)
	if err != nil {
		var status int
		if errors.Is(err, models.ErrApiKeyNotFound) {
			status = 404
		} else {
			status = 500
		}
		return response.Error(status, "Failed to delete API key", err)
	}

	return response.Success("API key deleted")
}

// swagger:route POST /auth/keys api_keys addAPIkey
//
// Creates an API key.
//
// Will return details of the created API key
//
// Responses:
// 200: postAPIkeyResponse
// 400: badRequestError
// 401: unauthorisedError
// 403: forbiddenError
// 409: conflictError
// 500: internalServerError
func (hs *HTTPServer) AddAPIKey(c *models.ReqContext) response.Response {
	cmd := models.AddApiKeyCommand{}
	if err := web.Bind(c.Req, &cmd); err != nil {
		return response.Error(http.StatusBadRequest, "bad request data", err)
	}
	if !cmd.Role.IsValid() {
		return response.Error(400, "Invalid role specified", nil)
	}
	if !c.OrgRole.Includes(cmd.Role) {
		return response.Error(http.StatusForbidden, "Cannot assign a role higher than user's role", nil)
	}

	if hs.Cfg.ApiKeyMaxSecondsToLive != -1 {
		if cmd.SecondsToLive == 0 {
			return response.Error(400, "Number of seconds before expiration should be set", nil)
		}
		if cmd.SecondsToLive > hs.Cfg.ApiKeyMaxSecondsToLive {
			return response.Error(400, "Number of seconds before expiration is greater than the global limit", nil)
		}
	}

	cmd.OrgId = c.OrgId

	newKeyInfo, err := apikeygen.New(cmd.OrgId, cmd.Name)
	if err != nil {
		return response.Error(500, "Generating API key failed", err)
	}

	cmd.Key = newKeyInfo.HashedKey
	if err := hs.SQLStore.AddAPIKey(c.Req.Context(), &cmd); err != nil {
		if errors.Is(err, models.ErrInvalidApiKeyExpiration) {
			return response.Error(400, err.Error(), nil)
		}
		if errors.Is(err, models.ErrDuplicateApiKey) {
			return response.Error(409, err.Error(), nil)
		}
		return response.Error(500, "Failed to add API Key", err)
	}

	result := &dtos.NewApiKeyResult{
		ID:   cmd.Result.Id,
		Name: cmd.Result.Name,
		Key:  newKeyInfo.ClientSecret,
	}

	return response.JSON(http.StatusOK, result)
}

// swagger:parameters getAPIkeys
type GetAPIkeysParams struct {
	// Show expired keys
	// in:query
	// required:false
	// default:false
	IncludeExpired bool `json:"includeExpired"`
}

// swagger:parameters addAPIkey
type AddAPIkeyParams struct {
	// in:body
	// required:true
	Body models.AddApiKeyCommand
}

// swagger:parameters deleteAPIkey
type DeleteAPIkeyParams struct {
	// in:path
	// required:true
	ID int64 `json:"id"`
}

// swagger:response getAPIkeyResponse
type GetAPIkeyResponse struct {
	// The response message
	// in: body
	Body []*dtos.ApiKeyDTO `json:"body"`
}

// swagger:response postAPIkeyResponse
type PostAPIkeyResponse struct {
	// The response message
	// in: body
	Body dtos.NewApiKeyResult `json:"body"`
}
