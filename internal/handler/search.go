package handler

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/store"
)

func Search(searcher search.Searcher) fiber.Handler {
	return func(c *fiber.Ctx) error {
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			return apierr.NewAPIError(fiber.StatusBadRequest, "q query parameter is required").Respond(c)
		}

		types, err := searchEntityTypes(c.Query("type"))
		if err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
		}
		projectIDs, err := getRequestViewerProjectIDs(c)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}

		references, err := searcher.Search(c.Context(), search.Request{
			Text:       query,
			ProjectIDs: projectIDs,
			Types:      types,
			Limit:      search.DefaultLimit,
		})
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		results, err := store.GetSearchResults(c.Context(), references, projectIDs)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		return c.JSON(fiber.Map{"results": results})
	}
}

func searchEntityTypes(value string) ([]model.SearchEntityType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return model.AllSearchEntityTypes(), nil
	case string(model.SearchEntityPrompt):
		return []model.SearchEntityType{model.SearchEntityPrompt}, nil
	case string(model.SearchEntitySkill):
		return []model.SearchEntityType{model.SearchEntitySkill}, nil
	case string(model.SearchEntityTool):
		return []model.SearchEntityType{model.SearchEntityTool}, nil
	default:
		return nil, fmt.Errorf("type must be one of: prompt, skill, tool")
	}
}
