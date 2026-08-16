// Package api exposes an HTTP JSON API for the todo app.
package api

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/greboid/todo/internal/db"
	"github.com/greboid/todo/internal/filter"
	"github.com/greboid/todo/internal/models"
	"github.com/greboid/todo/internal/schedule"
)

// openapiYAML is the embedded OpenAPI 3.0 spec describing the /api surface.
//
//go:embed openapi.yaml
var openapiYAML []byte

// swaggerFS embeds the third-party Swagger UI assets (swagger-ui-bundle.js and
// swagger-ui.css) copied from the `swagger-ui-dist` npm package by `pnpm run
// copy:swagger`. They are build artifacts; the docs page itself is rendered from
// swaggerPageTpl, not stored here.
//
//go:embed all:swagger-ui
var swaggerFS embed.FS

// swaggerUIFiles is the swagger-ui subtree of swaggerFS, served as static files.
var swaggerUIFiles = func() fs.FS {
	sub, err := fs.Sub(swaggerFS, "swagger-ui")
	if err != nil {
		panic(err)
	}
	return sub
}()

// swaggerAssets serves the embedded Swagger UI files under /api/swagger/.
var swaggerAssets = http.StripPrefix("/api/swagger/", http.FileServerFS(swaggerUIFiles))

// swaggerPageTpl is the Swagger UI page, rendered so the spec URL is injected
// and the HTML stays in committed Go source. Only the third-party JS/CSS live
// in the gitignored swagger-ui build dir. The inline script mirrors the
// system colour-scheme preference onto the html.dark-mode class that the dark
// palette bundled inside swagger-ui.css is keyed to, tracking OS changes live;
// the small style block covers the canvas and native form controls that the
// bundled palette leaves out.
var swaggerPageTpl = template.Must(template.New("swagger").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Todo API &mdash; Swagger UI</title>
  <link rel="stylesheet" href="swagger-ui.css">
  <script>
    (function () {
      var mq = window.matchMedia("(prefers-color-scheme: dark)");
      var apply = function () {
        document.documentElement.classList.toggle("dark-mode", mq.matches);
      };
      apply();
      mq.addEventListener("change", apply);
    })();
  </script>
  <style>
    html.dark-mode { color-scheme: dark; }
    html.dark-mode body { background: #1c2022; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="swagger-ui-bundle.js"></script>
  <script>
    window.addEventListener("DOMContentLoaded", function () {
      window.ui = SwaggerUIBundle({
        url: "{{.SpecURL}}",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
        layout: "BaseLayout"
      });
    });
  </script>
</body>
</html>`))

// Handler wires the todo HTTP routes. It holds no per-request state beyond
// the event bus shared by SSE clients.
type Handler struct {
	store   *db.DB
	apiKey  string
	sessKey []byte
	bus     *eventBus
}

// New returns a configured Handler. apiKey is optional: when empty the API is
// open; when set, requests must present the key (header) or a browser
// session cookie (see session.go).
func New(store *db.DB, apiKey string) *Handler {
	h := &Handler{store: store, apiKey: apiKey, bus: newEventBus()}
	if apiKey != "" {
		h.sessKey = sessionKey(apiKey)
	}
	return h
}

// Routes returns the API routes registered under /api, wrapped in the
// optional API-key guard. Uses Go 1.22+ method+path patterns so path
// parameters are available via r.PathValue.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/todos", h.listTodos)
	mux.HandleFunc("POST /api/todos", h.createTodo)
	mux.HandleFunc("GET /api/todos/{id}", h.getTodo)
	mux.HandleFunc("PATCH /api/todos/{id}", h.updateTodo)
	mux.HandleFunc("DELETE /api/todos/{id}", h.deleteTodo)
	mux.HandleFunc("POST /api/todos/{id}/move", h.moveTodo)
	mux.HandleFunc("POST /api/todos/{id}/complete", h.completeTodo)
	mux.HandleFunc("POST /api/schedule/parse", h.parseSchedule)
	mux.HandleFunc("POST /api/schedule/extract", h.extractSchedule)
	mux.HandleFunc("GET /api/labels", h.listLabels)
	mux.HandleFunc("PUT /api/labels/{name}", h.updateLabel)
	mux.HandleFunc("DELETE /api/labels/{name}", h.deleteLabel)
	mux.HandleFunc("POST /api/labels/predefined", h.addPredefinedLabel)
	mux.HandleFunc("DELETE /api/labels/predefined/{name}", h.removePredefinedLabel)
	mux.HandleFunc("GET /api/priorities", h.listPriorities)
	mux.HandleFunc("PUT /api/priorities/{name}", h.updatePriority)
	mux.HandleFunc("POST /api/priorities/predefined", h.addPredefinedPriority)
	mux.HandleFunc("POST /api/priorities/reorder", h.reorderPriorities)
	mux.HandleFunc("DELETE /api/priorities/predefined/{name}", h.removePredefinedPriority)
	mux.HandleFunc("GET /api/boards", h.listBoards)
	mux.HandleFunc("POST /api/boards", h.createBoard)
	mux.HandleFunc("GET /api/boards/{id}", h.getBoard)
	mux.HandleFunc("PATCH /api/boards/{id}", h.updateBoard)
	mux.HandleFunc("DELETE /api/boards/{id}", h.deleteBoard)
	mux.HandleFunc("GET /api/saved-searches", h.listSavedSearches)
	mux.HandleFunc("POST /api/saved-searches", h.createSavedSearch)
	mux.HandleFunc("DELETE /api/saved-searches/{id}", h.deleteSavedSearch)
	// Live sync: an SSE stream that pokes clients after any mutation.
	mux.HandleFunc("GET /api/events", h.streamEvents)
	// API docs: embedded OpenAPI spec and a self-hosted Swagger UI.
	mux.HandleFunc("GET /api/openapi.yaml", h.openapiSpec)
	mux.HandleFunc("GET /api/swagger/", h.swaggerUI)
	mux.HandleFunc("GET /api/swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/swagger/", http.StatusMovedPermanently)
	})
	// notifyMutations sits inside the key guard so unauthorized mutation
	// attempts never poke anyone.
	return h.requireAPIKey(h.notifyMutations(mux))
}

// Close tears down the handler's shared infrastructure: it ends every open
// SSE stream so a graceful server shutdown settles immediately instead of
// idling out its deadline on long-lived event connections. Call once, when
// the server is shutting down.
func (h *Handler) Close() {
	h.bus.close()
}

// requireAPIKey wraps next so that, when a key is configured, requests must
// present it — either in an X-API-Key header, as the Bearer token of the
// Authorization header, or as a valid browser session cookie (minted with
// the SPA document; see session.go). With no key configured (the default) it
// is a no-op and the API stays open. Key comparison is constant-time.
func (h *Handler) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		presented := r.Header.Get("X-API-Key")
		if presented == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				presented = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if presented != "" {
			if subtle.ConstantTimeCompare([]byte(presented), []byte(h.apiKey)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		} else if h.validSession(r) {
			// Sliding expiration: re-mint so active tabs outlive sessionTTL.
			h.MintSession(w, r)
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="todo api"`)
		writeErr(w, http.StatusUnauthorized, errors.New("missing or invalid API key"))
	})
}

func (h *Handler) listTodos(w http.ResponseWriter, r *http.Request) {
	// Optional ?boardId=N scoping. Without it, all todos across boards are
	// returned (back-compat with older clients and with the cascade sync path).
	boardID := int64(0)
	if raw := r.URL.Query().Get("boardId"); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			boardID = id
		}
	}
	var (
		todos []models.Todo
		err   error
	)
	// ?filter=<text> moves all filter parsing/matching server-side. The client
	// sends its local ?today (YYYY-MM-DD) so date presets resolve from the
	// user's perspective; it defaults to the server's date when absent. A
	// malformed filter is a 400 so the UI can surface the error rather than
	// silently dropping the token.
	if f := r.URL.Query().Get("filter"); f != "" {
		parsed, perr := filter.Parse(f)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, perr)
			return
		}
		today := r.URL.Query().Get("today")
		if today == "" {
			today = time.Now().Format("2006-01-02")
		}
		todos, err = h.store.ListFiltered(r.Context(), boardID, parsed, today)
	} else {
		todos, err = h.store.ListAll(r.Context(), boardID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(enrichAll(todos)))
}

func (h *Handler) createTodo(w http.ResponseWriter, r *http.Request) {
	var in models.CreateTodo
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, errors.New("title is required"))
		return
	}
	if err := validateDueRecurrence(in.DueDate, in.Recurrence); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Labels == nil {
		in.Labels = []string{}
	}
	in.Priority = strings.TrimSpace(in.Priority)
	t, err := h.store.Create(r.Context(), in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrich(t))
}

func (h *Handler) getTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrich(t))
}

func (h *Handler) updateTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in models.UpdateTodo
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := validateDueRecurrence(in.DueDate, in.Recurrence); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := h.store.Update(r.Context(), id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrich(t))
}

func (h *Handler) deleteTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) moveTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in models.MoveTodo
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := h.store.Move(r.Context(), id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrich(t))
}

func (h *Handler) completeTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in models.CompleteTodo
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := h.store.SetCompleted(r.Context(), id, in.Completed)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// Return the whole tree so the client can sync cascaded descendants.
	all, err := h.store.ListAll(r.Context(), t.BoardID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"todo": enrich(t), "todos": nonNil(enrichAll(all))})
}

func (h *Handler) listLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := h.store.ListLabels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(labels))
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (h *Handler) updateLabel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	var in models.UpdateLabel
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	color := ""
	if in.Color != nil {
		color = strings.TrimSpace(*in.Color)
		if color != "" && !hexColorRe.MatchString(color) {
			writeErr(w, http.StatusBadRequest, errors.New("color must be a hex string like #ef4444"))
			return
		}
	}
	if err := h.store.SetLabelColor(r.Context(), name, color); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.Label{Name: name, Color: color})
}

func (h *Handler) addPredefinedLabel(w http.ResponseWriter, r *http.Request) {
	var in models.CreateLabel
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if err := h.store.AddPredefinedLabel(r.Context(), in.Name); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": in.Name})
}

func (h *Handler) removePredefinedLabel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if err := h.store.RemovePredefinedLabel(r.Context(), name); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteLabel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if err := h.store.DeleteLabel(r.Context(), name); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listPriorities(w http.ResponseWriter, r *http.Request) {
	priorities, err := h.store.ListPriorities(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(priorities))
}

func (h *Handler) updatePriority(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	var in models.UpdatePriority
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	color := ""
	if in.Color != nil {
		color = strings.TrimSpace(*in.Color)
		if color != "" && !hexColorRe.MatchString(color) {
			writeErr(w, http.StatusBadRequest, errors.New("color must be a hex string like #ef4444"))
			return
		}
	}
	if err := h.store.SetPriorityColor(r.Context(), name, color); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models.Priority{Name: name, Color: color})
}

func (h *Handler) addPredefinedPriority(w http.ResponseWriter, r *http.Request) {
	var in models.CreatePriority
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if err := h.store.AddPredefinedPriority(r.Context(), in.Name); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": in.Name})
}

func (h *Handler) removePredefinedPriority(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if err := h.store.RemovePredefinedPriority(r.Context(), name); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reorderPriorities(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Names []string `json:"names"`
	}
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Names == nil {
		in.Names = []string{}
	}
	if err := h.store.ReorderPriorities(r.Context(), in.Names); err != nil {
		writeStoreErr(w, err)
		return
	}
	priorities, err := h.store.ListPriorities(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(priorities))
}

func (h *Handler) listBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := h.store.ListBoards(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(boards))
}

func (h *Handler) createBoard(w http.ResponseWriter, r *http.Request) {
	var in models.CreateBoard
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	b, err := h.store.CreateBoard(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *Handler) getBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, err := h.store.GetBoard(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) updateBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in models.UpdateBoard
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, err := h.store.UpdateBoard(r.Context(), id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) deleteBoard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteBoard(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listSavedSearches(w http.ResponseWriter, r *http.Request) {
	searches, err := h.store.ListSavedSearches(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(searches))
}

// createSavedSearch stores a filter query under a name. The query must parse
// with the filter grammar so a saved search is always appliable later.
func (h *Handler) createSavedSearch(w http.ResponseWriter, r *http.Request) {
	var in models.CreateSavedSearch
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		writeErr(w, http.StatusBadRequest, errors.New("query is required"))
		return
	}
	if _, err := filter.Parse(in.Query); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s, err := h.store.CreateSavedSearch(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (h *Handler) deleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteSavedSearch(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// helpers ----------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty request body")
		}
		return err
	}
	return nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

// openapiSpec serves the embedded OpenAPI 3.0 spec as YAML.
func (h *Handler) openapiSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openapiYAML)
}

// swaggerUI renders the Swagger UI page at the directory root and serves the
// embedded third-party JS/CSS for any sub-path.
func (h *Handler) swaggerUI(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/api/swagger/") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = swaggerPageTpl.Execute(w, struct{ SpecURL string }{SpecURL: "/api/openapi.yaml"})
		return
	}
	swaggerAssets.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}

type apiError struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: err.Error()})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrBoardNotFound),
		errors.Is(err, db.ErrSavedSearchNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, db.ErrNoBoard),
		errors.Is(err, db.ErrSelfParent),
		errors.Is(err, db.ErrCycle),
		errors.Is(err, db.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, db.ErrLastBoard):
		writeErr(w, http.StatusConflict, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

// validateDueRecurrence checks the optional due date and recurrence fields
// shared by create/update inputs. dueDate is accepted as "" (no check); a
// non-empty value must parse as YYYY-MM-DD. A recurrence, when present, must
// satisfy Recurrence.Valid. Both failures map to HTTP 400 via ErrInvalidInput.
func validateDueRecurrence(due *string, rc *models.Recurrence) error {
	if due != nil && *due != "" {
		if _, err := time.Parse("2006-01-02", *due); err != nil {
			return fmt.Errorf("%w: dueDate must be YYYY-MM-DD", db.ErrInvalidInput)
		}
	}
	if rc != nil && !rc.Valid() {
		return fmt.Errorf("%w: invalid recurrence", db.ErrInvalidInput)
	}
	return nil
}

// parseSchedule turns a free-text due/recurrence string into the structured
// representation the rest of the API uses. The client sends its local "today"
// (YYYY-MM-DD) so relative dates like "tomorrow" resolve from the user's
// perspective, not the server's UTC clock. Always returns 200 with an ok flag:
// ok=false carries an error string for live typing feedback without throwing.
func (h *Handler) parseSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Today string `json:"today,omitempty"`
	}
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	now := time.Now().UTC()
	if in.Today != "" {
		if t, err := time.Parse("2006-01-02", in.Today); err == nil {
			now = t
		}
	}
	type parseResponse struct {
		OK           bool               `json:"ok"`
		DueDate      string             `json:"dueDate"`
		Recurrence   *models.Recurrence `json:"recurrence"`
		ScheduleText string             `json:"scheduleText"`
		Error        string             `json:"error,omitempty"`
	}
	sched, err := schedule.Parse(in.Text, now)
	resp := parseResponse{OK: true, DueDate: sched.DueDate, Recurrence: sched.Recurrence, ScheduleText: schedule.FormatSchedule(sched.DueDate, sched.Recurrence, now)}
	if err != nil {
		resp = parseResponse{OK: false, Error: err.Error()}
	}
	writeJSON(w, http.StatusOK, resp)
}

// extractSchedule splits a quick-add string into a title, any #label tags, and
// an optional trailing due/recurrence. The client sends its local "today" so
// relative dates resolve from the user's perspective. Always returns 200; ok
// is false when the title would be empty (blank input or only labels).
func (h *Handler) extractSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Today string `json:"today,omitempty"`
	}
	if err := decode(w, r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	now := time.Now().UTC()
	if in.Today != "" {
		if t, err := time.Parse("2006-01-02", in.Today); err == nil {
			now = t
		}
	}
	qa, ok := schedule.Extract(in.Text, now)
	type extractResponse struct {
		OK           bool               `json:"ok"`
		Title        string             `json:"title"`
		Labels       []string           `json:"labels"`
		Priority     string             `json:"priority,omitempty"`
		DueDate      string             `json:"dueDate,omitempty"`
		Recurrence   *models.Recurrence `json:"recurrence,omitempty"`
		ScheduleText string             `json:"scheduleText,omitempty"`
	}
	resp := extractResponse{OK: ok, Title: qa.Title, Labels: nonNil(qa.Labels), Priority: qa.Priority}
	if ok && (qa.Schedule.DueDate != "" || qa.Schedule.Recurrence != nil) {
		resp.DueDate = qa.Schedule.DueDate
		resp.Recurrence = qa.Schedule.Recurrence
		resp.ScheduleText = schedule.FormatSchedule(qa.Schedule.DueDate, qa.Schedule.Recurrence, now)
	}
	writeJSON(w, http.StatusOK, resp)
}

// enrich stamps the computed scheduleText (edit-field seed) and recurrenceLabel
// (badge) onto a todo. These are presentation fields derived from the stored
// dueDate/recurrence, never persisted, so the schedule grammar lives only here.
func enrich(t models.Todo) models.Todo {
	if t.Recurrence != nil {
		t.RecurrenceLabel = schedule.Format(*t.Recurrence)
	}
	t.ScheduleText = schedule.FormatSchedule(t.DueDate, t.Recurrence, time.Now().UTC())
	return t
}

// enrichAll stamps every todo in place.
func enrichAll(ts []models.Todo) []models.Todo {
	for i := range ts {
		ts[i] = enrich(ts[i])
	}
	return ts
}

// nonNil returns a non-nil slice so JSON serializes [] rather than null.
// Without this, Go encodes a nil slice as the JSON token `null`, which the
// frontend treats specially.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
