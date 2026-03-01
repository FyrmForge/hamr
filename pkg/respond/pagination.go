package respond

import (
	"strconv"

	"github.com/labstack/echo/v4"
)

// Page holds pagination metadata.
type Page struct {
	Number     int  `json:"number"`
	Size       int  `json:"size"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
}

// PagedResponse wraps a slice of items with pagination metadata.
type PagedResponse[T any] struct {
	Data []T  `json:"data"`
	Page Page `json:"page"`
}

// ParsePagination reads page and size query params from the request.
// It defaults page to 1 and clamps size to [1, 100].
func ParsePagination(c echo.Context, defaultSize int) (page, size int) {
	page, _ = strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}

	size, _ = strconv.Atoi(c.QueryParam("size"))
	if size < 1 {
		size = defaultSize
	}
	if size > 100 {
		size = 100
	}

	return page, size
}

// NewPage computes pagination metadata from the given parameters.
func NewPage(page, size, total int) Page {
	if size <= 0 {
		return Page{Number: page, Size: size, Total: total}
	}

	totalPages := (total + size - 1) / size

	return Page{
		Number:     page,
		Size:       size,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1,
	}
}
