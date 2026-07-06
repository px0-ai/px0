package handler

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createTeamRequest struct {
	Name  string     `json:"name"`
	OrgID *uuid.UUID `json:"org_id,omitempty"`
}

type updateTeamRequest struct {
	Name  string     `json:"name"`
	OrgID *uuid.UUID `json:"org_id,omitempty"`
}

type addTeamMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

type updateTeamMemberRoleRequest struct {
	Role string `json:"role"`
}

func CreateTeam(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("orgID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createTeamRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	exists, err := store.TeamNameExistsInOrg(c.Context(), orgID, req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if exists {
		return apierr.NewAPIError(fiber.StatusConflict, "team name already exists under this organization").Respond(c)
	}

	team, err := store.CreateTeamWithOrg(c.Context(), req.Name, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.AddTeamMember(c.Context(), team.ID, userID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"team": team})
}

func UpdateTeam(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var isOrgAdmin bool
	if team.OrgID != nil {
		isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	isTeamEditor, err := store.IsTeamEditor(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isOrgAdmin && !isTeamAdmin && !isTeamEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateTeamRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	targetOrgID := team.OrgID
	if req.OrgID != nil {
		targetOrgID = req.OrgID
	}

	if targetOrgID != nil {
		exists, err := store.TeamNameExistsInOrgForOther(c.Context(), id, *targetOrgID, req.Name)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if exists {
			return apierr.NewAPIError(fiber.StatusConflict, "team name already exists under this organization").Respond(c)
		}
	}

	updatedTeam, err := store.UpdateTeam(c.Context(), id, req.Name, req.OrgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"team": updatedTeam})
}

func AddTeamMember(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var isOrgAdmin bool
	if team.OrgID != nil {
		isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isOrgAdmin && !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req addTeamMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.UserID == uuid.Nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "user_id is required").Respond(c)
	}

	// Verify the user being added actually exists in the database
	_, err = store.GetUserByID(c.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "user to add not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.AddTeamMember(c.Context(), teamID, req.UserID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func RemoveTeamMember(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	targetUserID, err := uuid.Parse(c.Params("userID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var isOrgAdmin bool
	if team.OrgID != nil {
		isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isOrgAdmin && !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	if team.OrgID != nil {
		inOrg, err := store.IsUserInOrg(c.Context(), targetUserID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if !inOrg {
			return apierr.NewAPIError(fiber.StatusBadRequest, "user is not a member of this organization").Respond(c)
		}
	}

	if err := store.RemoveTeamMember(c.Context(), teamID, targetUserID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func ListUserTeams(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	teams, err := store.GetUserTeams(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if teams == nil {
		teams = []*model.Team{}
	}
	return c.JSON(fiber.Map{"teams": teams})
}

func ListTeamMembers(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamViewer, err := store.IsTeamViewer(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamViewer {
		return apierr.ErrForbidden.Respond(c)
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page <= 0 {
		page = 1
	}
	limit := 10

	members, total, err := store.GetTeamMembersPaginated(c.Context(), teamID, page, limit)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if members == nil {
		members = []*model.TeamMemberResponse{}
	}

	return c.JSON(fiber.Map{
		"members": members,
		"page":    page,
		"limit":   limit,
		"total":   total,
	})
}

func UpdateTeamMemberRole(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	targetUserID, err := uuid.Parse(c.Params("userID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var isOrgAdmin bool
	if team.OrgID != nil {
		isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isOrgAdmin && !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateTeamMemberRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Role != "admin" && req.Role != "editor" && req.Role != "viewer" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid role, must be admin, editor, or viewer").Respond(c)
	}

	err = store.UpdateTeamMemberRole(c.Context(), teamID, targetUserID, req.Role)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team member relationship not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "role updated successfully",
	})
}

// ListOrgTeams lists all teams within a specific organization.
// Anyone belonging to that organization can see the teams.
func ListOrgTeams(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("orgID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	// Verify the user belongs to this organization
	belongs, err := store.IsUserInOrg(c.Context(), userID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// If not, verify if they are an Org Admin or system admin (they should be allowed to view)
	if !belongs {
		isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, orgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if !isOrgAdmin {
			return apierr.ErrForbidden.Respond(c)
		}
	}

	teams, err := store.GetTeamsByOrgID(c.Context(), orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if teams == nil {
		teams = []*model.Team{}
	}
	return c.JSON(fiber.Map{"teams": teams})
}

// CreateJoinRequest creates a pending request for the authenticated user to join a specific team.
func CreateJoinRequest(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	// Validate the team exists
	_, err = store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Check if the user is already a member of this team
	isMember, err := store.IsTeamViewer(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if isMember {
		var exists bool
		err = db.Pool.QueryRow(c.Context(), `SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2)`, teamID, userID).Scan(&exists)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if exists {
			return apierr.NewAPIError(fiber.StatusBadRequest, "already a member of this team").Respond(c)
		}
	}

	// Also verify that the user is verified
	u, err := store.GetUserByID(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !u.IsVerified {
		return apierr.ErrUserNotVerified.Respond(c)
	}

	req, err := store.CreateJoinRequest(c.Context(), teamID, userID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusConflict, "a pending join request already exists for this team").Respond(c)
	}

	return c.Status(fiber.StatusCreated).JSON(req)
}

// GetAdminInbox returns all pending join requests that the caller is authorized to approve or reject.
func GetAdminInbox(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	items, err := store.GetPendingJoinRequestsForAdmin(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if items == nil {
		items = []*model.InboxItem{}
	}
	return c.JSON(fiber.Map{"inbox": items})
}

type resolveJoinRequest struct {
	Status string `json:"status"` // "approved" or "rejected"
}

// ResolveJoinRequest allows an authorized admin to approve or reject a pending request.
func ResolveJoinRequest(c *fiber.Ctx) error {
	requestID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	var reqBody resolveJoinRequest
	if err := c.BodyParser(&reqBody); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	status := strings.ToLower(reqBody.Status)
	if status != "approved" && status != "rejected" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid status, must be approved or rejected").Respond(c)
	}

	// Fetch request
	req, err := store.GetJoinRequestByID(c.Context(), requestID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "join request not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if req.Status != "pending" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "join request has already been resolved").Respond(c)
	}

	// Verify caller is authorized to approve/reject:
	// They must be system admin, admin of the target team, or admin of some team in the same organization as the target team.
	sysAdmin, err := store.IsSystemAdmin(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	authorized := sysAdmin
	if !authorized {
		// Is team admin of the specific team?
		isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, req.TeamID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if isTeamAdmin {
			authorized = true
		} else {
			// Is org admin (admin of some team in same org)?
			team, err := store.GetTeamByID(c.Context(), req.TeamID)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			if team.OrgID != nil {
				isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
				if err != nil {
					return apierr.ErrInternalError.Respond(c, err)
				}
				if isOrgAdmin {
					authorized = true
				}
			}
		}
	}

	if !authorized {
		return apierr.ErrForbidden.Respond(c)
	}

	// Update request status
	resolvedReq, err := store.UpdateJoinRequestStatus(c.Context(), requestID, status)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// If approved, add the user to the team!
	if status == "approved" {
		err = store.AddTeamMember(c.Context(), req.TeamID, req.UserID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	return c.JSON(resolvedReq)
}

// DeleteTeam deletes an existing team.
func DeleteTeam(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var isOrgAdmin bool
	if team.OrgID != nil {
		isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	isTeamEditor, err := store.IsTeamEditor(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isOrgAdmin && !isTeamAdmin && !isTeamEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	if err := store.DeleteTeam(c.Context(), id); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func LeaveTeam(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	// Verify that user is a member of the team
	isMember, err := store.IsTeamViewer(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isMember {
		return apierr.NewAPIError(fiber.StatusNotFound, "user is not a member of this team").Respond(c)
	}

	// Remove membership
	if err := store.RemoveTeamMember(c.Context(), teamID, userID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}
