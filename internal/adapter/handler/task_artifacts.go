package handler

// task_artifacts.go — cycle-8 L9 (tasks-plugins-02/17/19): a metadata-only
// listing of a task's result URLs (GET .../artifacts) and a byte proxy for
// one of them (GET .../artifacts/:key/content), generalising video_proxy.go
// beyond video so any of the generic-task-surface platforms (task-router.go,
// task_generic.go) can expose whatever URLs its vendor wrote into Task.Data
// — not just the video-only channel types video_proxy.go already serves via
// its own dedicated /v1/videos/:task_id/content route, which stays
// untouched (see video_proxy.go's file comment and video-router.go).
//
// Scope: Task.Data plus, for a SUCCESS task, task.FailReason (see
// artifactURLsForTask below). The plan text says "Task.Data/PrivateData",
// but entity.TaskPrivateData (domain/entity/task.go) today holds exactly
// one field — an upstream API key video_proxy.go's Gemini branch uses to
// authenticate ITS OWN fetch — never a result URL, so there is nothing in
// PrivateData to list.
//
// Ownership: both handlers here call respondTaskNotFound (defined in
// task_generic.go) for both "no such task_id" and "task belongs to someone
// else", the same fail-closed 404 body GetTaskGeneric answers with, so the
// two routes here can't be used to probe which task ids exist either. The
// lookup itself differs from GetTaskGeneric's — see loadOwnedTask below.
import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// TaskArtifact is one entry of GET .../artifacts's data array.
type TaskArtifact struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Status   string `json:"status"`
}

// loadOwnedTask is shared by both handlers in this file so the two artifact
// routes fail closed the same way the status route does. Unlike
// GetTaskGeneric's repo.GetByTaskId(requesterID, taskID) (SQL-scoped to the
// caller), this fetches by task_id alone (repo.GetByOnlyTaskId) and checks
// ownership afterwards in Go — same externally-visible 404-or-owned outcome
// for a non-privileged caller, but not the same lookup shape. It writes the
// response itself on any non-ok outcome; callers just return when ok is
// false.
func loadOwnedTask(c *gin.Context) (task *repo.Task, ok bool) {
	platform := constant.TaskPlatform(c.GetString("platform"))
	taskID := c.Param("task_id")

	task, exists, err := repo.GetByOnlyTaskId(taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"type": "server_error", "message": "Failed to query task"},
		})
		return nil, false
	}
	if !exists || task == nil || task.Platform != platform {
		respondTaskNotFound(c)
		return nil, false
	}

	requesterID := c.GetInt("id")
	requesterRole := c.GetInt("role")
	isPrivileged := requesterRole >= common.RoleAdminUser
	if !isPrivileged && task.UserId != requesterID {
		respondTaskNotFound(c)
		return nil, false
	}
	return task, true
}

// looksLikeArtifactURL is the filter extractArtifactURLs applies to a
// string value: only http(s)/data URLs are "result URLs" worth listing —
// an arbitrary string field (a title, a prompt echo) is not an artifact.
func looksLikeArtifactURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "data:")
}

// extractArtifactURLs walks task.Data for string values that look like a
// result URL. It only descends one level: a top-level JSON object's
// string-valued fields, or a top-level JSON array's string elements
// (index-based key, per the plan's "key is stable (index-based or
// vendor-provided id)"). This flat shape is what suno/music's Data actually
// is (task.go:239's faultsim.go simulator and dto.SunoDataResponse.Data's
// real-vendor payload) — it is NOT what the other nine compiled task
// adaptors' Data is: ali/adaptor.go's Output.VideoURL, kling/adaptor.go's
// Data.TaskResult.Videos[0].Url, jimeng/doubao/vidu/hailuo/vertex likewise
// nest their result URL inside Data, and the poller (task_video.go's
// updateVideoSingleTask) stores the RESOLVED url in task.FailReason, not in
// a top-level Data field — see artifactURLsForTask below, which is what
// both handlers in this file actually call; this function alone is empty
// for those nine platforms' Data. Nested objects/arrays beyond one level
// are not walked here: guessing at an unknown vendor's nesting is exactly
// the kind of invented behaviour this repo's prose rules forbid documenting
// as supported, and the nine platforms' real result URL is recovered via
// FailReason instead, not by walking deeper into Data.
func extractArtifactURLs(data json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(data) == 0 {
		return out
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		for k, v := range obj {
			if s, isStr := v.(string); isStr && looksLikeArtifactURL(s) {
				out[k] = s
			}
		}
		return out
	}
	var arr []any
	if err := json.Unmarshal(data, &arr); err == nil {
		for i, v := range arr {
			if s, isStr := v.(string); isStr && looksLikeArtifactURL(s) {
				out[strconv.Itoa(i)] = s
			}
		}
	}
	return out
}

// failReasonArtifactExcludedRoutes are the :platform route names whose
// task.FailReason is NOT the vendor's own asset URL, so projecting it as an
// artifact would be either self-referential or pointless: sora and gemini
// (task/sora/adaptor.go and task/gemini/adaptor.go's ParseTaskResult) both
// set taskResult.Url — which task_video.go's updateVideoSingleTask then
// stores into FailReason on SUCCESS — to this GATEWAY'S OWN
// "%s/v1/videos/:task_id/content" URL (system_setting.ServerAddress), the
// address VideoProxy itself serves, not a vendor CDN link. Synthesizing a
// "video" artifact from that value would either point back at
// VideoProxy (a route this file does not proxy to) or, if resolved
// naively, loop.
var failReasonArtifactExcludedRoutes = map[string]bool{
	"sora":   true,
	"gemini": true,
}

// failReasonArtifactKey is the stable key a SUCCESS task's task.FailReason
// is projected under, mirroring the reference project's own "video" key for
// its equivalent legacy-video artifact (controller/task.go's
// legacyVideoAvailable/GetResultURL in the upstream this repo tracks).
const failReasonArtifactKey = "video"

// artifactURLsForTask is what ListTaskArtifacts/GetTaskArtifactContent
// actually call: extractArtifactURLs(task.Data) — which alone is enough for
// suno/music (flat Data), see that function's doc comment — PLUS, for a
// SUCCESS task on a route not in failReasonArtifactExcludedRoutes, a
// synthetic "video" entry from task.FailReason when it looks like an
// artifact URL and Data did not already produce an entry under that key.
// This is where the ali/kling/jimeng/doubao/vidu/hailuo/vertex adaptors'
// resolved result URL actually lives (task_video.go:150's `task.FailReason
// = taskResult.Url`; video_proxy.go's own `default:` branch reads the
// identical field for the same reason) — without this, the listing is an
// empty array for every SUCCESS task on those platforms even though
// video_proxy.go can serve the very same URL.
func artifactURLsForTask(routeName string, task *repo.Task) map[string]string {
	urls := extractArtifactURLs(task.Data)
	if _, has := urls[failReasonArtifactKey]; !has &&
		task.Status == repo.TaskStatusSuccess &&
		!failReasonArtifactExcludedRoutes[routeName] &&
		looksLikeArtifactURL(task.FailReason) {
		urls[failReasonArtifactKey] = task.FailReason
	}
	return urls
}

// mimeTypeToArtifactType maps a MIME type (when known) or otherwise the
// artifact's own key name (matching the vendor "…_url" naming convention
// dto.SunoSong's VideoURL/AudioURL/ImageURL fields use) onto one of the six
// documented artifact types.
func mimeTypeToArtifactType(key, mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "text/"):
		return "text"
	case mimeType == "application/json":
		return "json"
	}
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "video"):
		return "video"
	case strings.Contains(lower, "image"):
		return "image"
	case strings.Contains(lower, "audio"), strings.Contains(lower, "music"):
		return "audio"
	case strings.Contains(lower, "text"):
		return "text"
	case strings.Contains(lower, "json"):
		return "json"
	}
	return "other"
}

// classifyArtifact resolves the {type, mime_type, size} triple for one
// artifact URL. size is only ever non-zero for a data: URL, where the
// decoded payload is already in hand for free; an http(s) URL's size is
// always reported as 0 (unknown) because this listing deliberately never
// performs a network call to find out — "metadata only, no bytes" per the
// plan.
func classifyArtifact(key, rawURL string) (artifactType, mimeType string, size int64) {
	if strings.HasPrefix(rawURL, "data:") {
		mt, decoded, ok := parseDataURL(rawURL)
		if ok {
			mimeType = mt
			size = int64(len(decoded))
		}
		return mimeTypeToArtifactType(key, mimeType), mimeType, size
	}
	if u, err := url.Parse(rawURL); err == nil {
		guessed := mime.TypeByExtension(path.Ext(u.Path))
		if semi := strings.IndexByte(guessed, ';'); semi >= 0 {
			guessed = guessed[:semi]
		}
		mimeType = guessed
	}
	return mimeTypeToArtifactType(key, mimeType), mimeType, 0
}

// ListTaskArtifacts serves GET /v1/tasks/:platform/:task_id/artifacts.
func ListTaskArtifacts(c *gin.Context) {
	task, ok := loadOwnedTask(c)
	if !ok {
		return
	}

	urls := artifactURLsForTask(c.Param("platform"), task)
	keys := make([]string, 0, len(urls))
	for k := range urls {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic listing order

	artifacts := make([]TaskArtifact, 0, len(keys))
	for _, k := range keys {
		artifactType, mimeType, size := classifyArtifact(k, urls[k])
		artifacts = append(artifacts, TaskArtifact{
			Key: k, Type: artifactType, MimeType: mimeType, Size: size,
			Status: string(task.Status),
		})
	}
	common.ApiSuccess(c, artifacts)
}

// GetTaskArtifactContent serves
// GET /v1/tasks/:platform/:task_id/artifacts/:key/content. The key must be
// one artifactURLsForTask would currently produce for this task — an
// unrecognised key (never issued, or an artifact that disappeared because
// the vendor payload changed) is a 404, not a 400: from the caller's side
// it looks exactly like asking for a key the listing never offered.
func GetTaskArtifactContent(c *gin.Context) {
	task, ok := loadOwnedTask(c)
	if !ok {
		return
	}

	urls := artifactURLsForTask(c.Param("platform"), task)
	rawURL, found := urls[c.Param("key")]
	if !found {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"type": "invalid_request_error", "message": "Artifact not found", "code": string(types.ErrorCodeArtifactNotFound)},
		})
		return
	}
	streamMediaContent(c, rawURL)
}
