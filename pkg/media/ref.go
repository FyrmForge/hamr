package media

import "fmt"

// ImageRef provides URL construction for a specific uploaded image.
// It is returned by GetMedia / GetMediaCtx on image stores.
type ImageRef struct {
	id       string
	category string
	format   string
	sizes    []ImageSize
	baseURL  string            // includes URL prefix or CDN base
	signFn   func(string) string // if non-nil, produces signed URLs
}

// Size returns the URL for the named size variant.
func (r ImageRef) Size(name string) string {
	path := fmt.Sprintf("%s/%s/%s.%s", r.category, r.id, name, r.format)
	if r.signFn != nil {
		return r.signFn(path)
	}
	return fmt.Sprintf("%s/%s", r.baseURL, path)
}

// Thumb returns the URL for the "thumb" size. If no thumb size is configured,
// it returns the smallest available size.
func (r ImageRef) Thumb() string {
	for _, s := range r.sizes {
		if s.Name == "thumb" {
			return r.Size("thumb")
		}
	}
	return r.Smallest()
}

// Smallest returns the URL for the smallest configured size (by width).
func (r ImageRef) Smallest() string {
	if len(r.sizes) == 0 {
		return ""
	}
	smallest := r.sizes[0]
	for _, s := range r.sizes[1:] {
		if s.Width < smallest.Width {
			smallest = s
		}
	}
	return r.Size(smallest.Name)
}

// Biggest returns the URL for the largest configured size (by width).
func (r ImageRef) Biggest() string {
	if len(r.sizes) == 0 {
		return ""
	}
	biggest := r.sizes[0]
	for _, s := range r.sizes[1:] {
		if s.Width > biggest.Width {
			biggest = s
		}
	}
	return r.Size(biggest.Name)
}

// VideoRef provides URL construction for a specific uploaded video.
// It is returned by GetMedia / GetMediaCtx on video stores.
type VideoRef struct {
	id           string
	category     string
	baseURL      string
	hasThumbnail bool
	signFn       func(string) string
}

// Video returns the URL for the video file.
func (r VideoRef) Video() string {
	path := fmt.Sprintf("%s/%s/video.mp4", r.category, r.id)
	if r.signFn != nil {
		return r.signFn(path)
	}
	return fmt.Sprintf("%s/%s", r.baseURL, path)
}

// Thumbnail returns the URL for the video thumbnail. Returns empty string if
// thumbnail generation was not enabled.
func (r VideoRef) Thumbnail() string {
	if !r.hasThumbnail {
		return ""
	}
	path := fmt.Sprintf("%s/%s/thumb.jpg", r.category, r.id)
	if r.signFn != nil {
		return r.signFn(path)
	}
	return fmt.Sprintf("%s/%s", r.baseURL, path)
}
