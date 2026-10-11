package app

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type CategoryRequest struct {
	Operation   string `json:"operation"`
	AccountID   string `json:"accountId"`
	Offset      int    `json:"offset"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     *bool  `json:"enabled"`
}

func (p CategoryRequest) Valid() bool {
	if len(p.AccountID) > 100 || len(p.ID) > 100 || p.Offset < 0 || p.Offset > 1000000 {
		return false
	}
	switch p.Operation {
	case "list":
		return p.ID == "" && p.Name == "" && p.Description == "" && p.Enabled == nil
	case "save":
		return p.AccountID != "" && p.Offset == 0 && p.Enabled != nil && strings.TrimSpace(p.Name) != "" && len(p.Name) <= 100 && strings.TrimSpace(p.Description) != "" && len(p.Description) <= 1000 && utf8.ValidString(p.Name) && utf8.ValidString(p.Description)
	case "delete":
		return p.AccountID != "" && p.ID != "" && p.Offset == 0 && p.Name == "" && p.Description == "" && p.Enabled == nil
	}
	return false
}

type CategoryDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}
type CategoryPage struct {
	AccountID string        `json:"accountId"`
	Rows      []CategoryDTO `json:"rows"`
	HasMore   bool          `json:"hasMore"`
	Code      string        `json:"code,omitempty"`
}

func (a *App) ManageCategories(ctx context.Context, p CategoryRequest) CategoryPage {
	result := CategoryPage{Rows: []CategoryDTO{}}
	if !p.Valid() {
		result.Code = "validation"
		return result
	}
	if a.Auth == nil || a.Categories == nil {
		result.Code = "unauthorized"
		return result
	}
	account, session, err := a.Auth.Session()
	if err != nil || (p.AccountID != "" && p.AccountID != string(account)) {
		result.Code = "unauthorized"
		return result
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	result.AccountID = string(account)
	switch p.Operation {
	case "save":
		if p.ID != "" {
			_, err = a.Categories.Get(ctx, account, domain.CategoryID(p.ID))
		}
		if err == nil {
			_, err = a.Categories.Save(ctx, account, domain.EmailCategory{ID: domain.CategoryID(p.ID), Name: p.Name, Description: p.Description, Enabled: *p.Enabled})
		}
	case "delete":
		err = a.Categories.Delete(ctx, account, domain.CategoryID(p.ID))
	}
	if err == nil {
		var rows []domain.EmailCategory
		rows, err = a.Categories.List(ctx, account)
		if err == nil {
			start := min(p.Offset, len(rows))
			end := min(start+5, len(rows))
			result.HasMore = end < len(rows)
			for _, c := range rows[start:end] {
				result.Rows = append(result.Rows, CategoryDTO{string(c.ID), c.Name, c.Description, c.Enabled, c.CreatedAt.UnixMilli(), c.UpdatedAt.UnixMilli()})
			}
		}
	}
	if session.Err() != nil {
		return CategoryPage{Rows: []CategoryDTO{}, Code: "unauthorized"}
	}
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrConflict):
			result.Code = "conflict"
		case errors.Is(err, repository.ErrNotFound):
			result.Code = "not_found"
		case errors.Is(err, repository.ErrInvalid):
			result.Code = "validation"
		default:
			result.Code = "storage_error"
		}
	}
	return result
}
