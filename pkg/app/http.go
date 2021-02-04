// +build !wasm

package app

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/maxence-charriere/go-app/v7/pkg/errors"
)

const (
	defaultThemeColor = "#2d2c2c"
)

// Handler is an HTTP handler that serves an HTML page that loads a Go wasm app
// and its resources.
type Handler struct {
	// The page authors.
	Author string

	// A placeholder background color for the application page to display before
	// its stylesheets are loaded.
	//
	// DEFAULT: #2d2c2c.
	BackgroundColor string

	// The path of the static resources that the browser is caching in order to
	// provide offline mode.
	//
	// Note that Icon, Styles and Scripts are already cached by default.
	//
	// Paths are relative to the root directory.
	CacheableResources []string

	// The page description.
	Description string

	// The environment variables that are passed to the progressive web app.
	//
	// Reserved keys:
	// - GOAPP_VERSION
	// - GOAPP_GOAPP_STATIC_RESOURCES_URL
	Env Environment

	// The icon that is used for the PWA, favicon, loading and default not
	// found component.
	Icon Icon

	// The page keywords.
	Keywords []string

	// The text displayed while loading a page.
	LoadingLabel string

	// The name of the web application as it is usually displayed to the user.
	Name string

	// The static resources that are accessible from custom paths. Files that
	// are proxied by default are /robots.txt, /sitemap.xml and /ads.txt.
	ProxyResources []ProxyResource

	// Additional headers to be added in head element.
	RawHeaders []string

	// The paths or urls of the JavaScript files to use with the page.
	//
	// eg:
	//  app.Handler{
	//      Scripts: []string{
	//          "/web/test.js",            // Static resource
	//          "https://foo.com/test.js", // External resource
	//      },
	//  },
	Scripts []string

	// The name of the web application displayed to the user when there is not
	// enough space to display Name.
	ShortName string

	// The resource provider that provides static resources. Static resources
	// are always accessed from a path that starts with "/web/".
	//
	// eg:
	//  "/web/main.css"
	//
	// Default: LocalDir("web")
	Resources ResourceProvider

	// The paths or urls of the CSS files to use with the page.
	//
	// eg:
	//  app.Handler{
	//      Styles: []string{
	//          "/web/test.css",            // Static resource
	//          "https://foo.com/test.css", // External resource
	//      },
	//  },
	Styles []string

	// The theme color for the application. This affects how the OS displays the
	// app (e.g., PWA title bar or Android's task switcher).
	//
	// DEFAULT: #2d2c2c.
	ThemeColor string

	// The page title.
	Title string

	// The version number. This is used in order to update the PWA application
	// in the browser. It must be set when deployed on a live system in order to
	// prevent recurring updates.
	//
	// Default: Auto-generated in order to trigger pwa update on a local
	// development system.
	Version string

	once             sync.Once
	etag             string
	appWasmPath      string
	robotPath        string
	hasRemoteRootDir bool
	page             bytes.Buffer

	resourcesMu    sync.RWMutex
	resources      map[string]httpResource
	proxyResources map[string]ProxyResource
}

func (h *Handler) init() {
	h.initVersion()
	h.initStaticResources()
	h.initStyles()
	h.initScripts()

	h.initCacheableResources()
	h.initIcon()
	h.initPWA()
	h.initPage()

	h.initResources()
	h.initProxyResources()
}

func (h *Handler) initVersion() {
	if h.Version == "" {
		t := time.Now().UTC().String()
		h.Version = fmt.Sprintf(`%x`, sha1.Sum([]byte(t)))
	}
	h.etag = `"` + h.Version + `"`
}

func (h *Handler) initStaticResources() {
	if h.Resources == nil {
		h.Resources = LocalDir("web")
	}
}

func (h *Handler) initStyles() {
	for i, path := range h.Styles {
		h.Styles[i] = h.staticResource(path)
	}
}

func (h *Handler) initScripts() {
	for i, path := range h.Scripts {
		h.Scripts[i] = h.staticResource(path)
	}
}

func (h *Handler) initCacheableResources() {
	for i, path := range h.CacheableResources {
		h.CacheableResources[i] = h.staticResource(path)
	}
}

func (h *Handler) initIcon() {
	if h.Icon.Default == "" {
		h.Icon.Default = "https://storage.googleapis.com/murlok-github/icon-192.png"
		h.Icon.Large = "https://storage.googleapis.com/murlok-github/icon-512.png"
	}

	if h.Icon.AppleTouch == "" {
		h.Icon.AppleTouch = h.Icon.Default
	}

	h.Icon.Default = h.staticResource(h.Icon.Default)
	h.Icon.Large = h.staticResource(h.Icon.Large)
	h.Icon.AppleTouch = h.staticResource(h.Icon.AppleTouch)
}

func (h *Handler) initPWA() {
	if h.Name == "" && h.ShortName == "" && h.Title == "" {
		h.Name = "App PWA"
	}
	if h.ShortName == "" {
		h.ShortName = h.Name
	}
	if h.Name == "" {
		h.Name = h.ShortName
	}

	if h.BackgroundColor == "" {
		h.BackgroundColor = defaultThemeColor
	}
	if h.ThemeColor == "" {
		h.ThemeColor = defaultThemeColor
	}

	if h.LoadingLabel == "" {
		h.LoadingLabel = "Loading"
	}
}

func (h *Handler) initPage() {
	h.page.WriteString("<!DOCTYPE html>\n")

	html := Html().Body(
		Head().Body(
			Meta().Charset("UTF-8"),
			Meta().
				HTTPEquiv("Content-Type").
				Content("text/html; charset=utf-8"),
			Meta().
				Name("author").
				Content(h.Author),
			Meta().
				Name("description").
				Content(h.Description),
			Meta().
				Name("keywords").
				Content(strings.Join(h.Keywords, ", ")),
			Meta().
				Name("theme-color").
				Content(h.ThemeColor),
			Meta().
				Name("viewport").
				Content("width=device-width, initial-scale=1, maximum-scale=1, user-scalable=0, viewport-fit=cover"),
			Title().Text(h.Title),
			Link().
				Rel("icon").
				Type("image/png").
				Href(h.Icon.Default),
			Link().
				Rel("apple-touch-icon").
				Href(h.Icon.AppleTouch),
			Link().
				Rel("manifest").
				Href(h.appResource("/manifest.webmanifest")),
			Link().
				Type("text/css").
				Rel("stylesheet").
				Href(h.appResource("/app.css")),
			Script().
				Defer(true).
				Src(h.appResource("/wasm_exec.js")),
			Script().
				Defer(true).
				Src(h.appResource("/app.js")),
			Range(h.Styles).Slice(func(i int) UI {
				return Link().
					Type("text/css").
					Rel("stylesheet").
					Href(h.Styles[i])
			}),
			Range(h.Scripts).Slice(func(i int) UI {
				return Script().
					Defer(true).
					Src(h.Scripts[i])
			}),
			Range(h.RawHeaders).Slice(func(i int) UI {
				return Raw(h.RawHeaders[i])
			}),
		),
		Body().Body(
			Div().
				ID("app-wasm-layout").
				Class("goapp-app-info").
				Body(
					Img().
						ID("app-wasm-loader-icon").
						Class("goapp-logo goapp-spin").
						Src(h.Icon.Default),
					P().
						ID("app-wasm-loader-label").
						Class("goapp-label").
						Body(Text(h.LoadingLabel)),
				),
			Div().ID("app-context-menu"),
			Div().ID("app-end"),
		),
	)

	html.html(&h.page)
}

func (h *Handler) initResources() {
	h.resources = make(map[string]httpResource)

	h.setResource("/wasm_exec.js", httpResource{
		ContentType: "application/javascript",
		Body:        stob(wasmExecJS),
	})

	h.setResource("/app.js", httpResource{
		ContentType: "application/javascript",
		Body:        h.makeAppJS(),
	})

	h.setResource("/app-worker.js", httpResource{
		ContentType: "application/javascript",
		Body:        h.makeAppWorkerJS(),
	})

	h.setResource("/manifest.webmanifest", httpResource{
		ContentType: "application/manifest+json",
		Body:        h.makeManifestJSON(),
	})

	h.setResource("/app.css", httpResource{
		ContentType: "text/css",
		Body:        stob(appCSS),
	})
}

func (h *Handler) makeAppJS() []byte {
	if h.Env == nil {
		h.Env = make(map[string]string, 2)
	}
	h.Env["GOAPP_VERSION"] = h.Version
	h.Env["GOAPP_STATIC_RESOURCES_URL"] = h.Resources.StaticResources()
	h.Env["GOAPP_ROOT_PREFIX"] = h.Resources.AppResources()

	env, err := json.Marshal(h.Env)
	if err != nil {
		panic(errors.New("encoding pwa env failed").
			Tag("env", h.Env).
			Wrap(err),
		)
	}

	var b bytes.Buffer
	if err := template.
		Must(template.New("app.js").Parse(appJS)).
		Execute(&b, struct {
			Env      string
			Wasm     string
			WorkerJS string
		}{
			Env:      btos(env),
			Wasm:     h.Resources.AppWASM(),
			WorkerJS: h.appResource("/app-worker.js"),
		}); err != nil {
		panic(errors.New("initializing app.js failed").Wrap(err))
	}
	return b.Bytes()
}

func (h *Handler) makeAppWorkerJS() []byte {
	cacheableResources := map[string]struct{}{
		h.appResource("/app.css"):              {},
		h.appResource("/app.js"):               {},
		h.appResource("/manifest.webmanifest"): {},
		h.appResource("/wasm_exec.js"):         {},
		h.appResource("/"):                     {},
		h.Resources.AppWASM():                  {},
		h.Icon.Default:                         {},
		h.Icon.Large:                           {},
		h.Icon.AppleTouch:                      {},
	}

	cacheResources := func(res []string) {
		for _, r := range res {
			cacheableResources[r] = struct{}{}
		}
	}
	cacheResources(h.Styles)
	cacheResources(h.Scripts)
	cacheResources(h.CacheableResources)

	var b bytes.Buffer
	if err := template.
		Must(template.New("app-worker.js").Parse(appWorkerJS)).
		Execute(&b, struct {
			Version          string
			ResourcesToCache map[string]struct{}
		}{
			Version:          h.Version,
			ResourcesToCache: cacheableResources,
		}); err != nil {
		panic(errors.New("initializing app-worker.js failed").Wrap(err))
	}
	return b.Bytes()
}

func (h *Handler) makeManifestJSON() []byte {
	normalize := func(s string) string {
		if !strings.HasPrefix(s, "/") {
			s = "/" + s
		}
		if !strings.HasSuffix(s, "/") {
			s += "/"
		}
		return s
	}

	var b bytes.Buffer
	if err := template.
		Must(template.New("manifest.webmanifest").Parse(manifestJSON)).
		Execute(&b, struct {
			ShortName       string
			Name            string
			DefaultIcon     string
			LargeIcon       string
			BackgroundColor string
			ThemeColor      string
			Scope           string
			StartURL        string
		}{
			ShortName:       h.ShortName,
			Name:            h.Name,
			DefaultIcon:     h.Icon.Default,
			LargeIcon:       h.Icon.Large,
			BackgroundColor: h.BackgroundColor,
			ThemeColor:      h.ThemeColor,
			Scope:           normalize(h.Resources.AppResources()),
			StartURL:        normalize(h.Resources.AppResources()),
		}); err != nil {
		panic(errors.New("initializing manifest.webmanifest failed").Wrap(err))
	}
	return b.Bytes()
}

func (h *Handler) setResource(path string, r httpResource) {
	r.Path = path
	h.resourcesMu.Lock()
	h.resources[path] = r
	h.resourcesMu.Unlock()
}

func (h *Handler) getResource(path string) (httpResource, bool) {
	h.resourcesMu.RLock()
	r, ok := h.resources[path]
	h.resourcesMu.RUnlock()
	return r, ok
}

func (h *Handler) initProxyResources() {
	resources := make(map[string]ProxyResource)

	for _, r := range h.ProxyResources {
		switch r.Path {
		case "/wasm_exec.js",
			"/goapp.js",
			"/app.js",
			"/app-worker.js",
			"/manifest.json",
			"/manifest.webmanifest",
			"/app.css",
			"/app.wasm",
			"/goapp.wasm",
			"/":
			continue

		default:
			if strings.HasPrefix(r.Path, "/") && strings.HasPrefix(r.ResourcePath, "/web/") {
				resources[r.Path] = r
			}
		}
	}

	if _, ok := resources["/robots.txt"]; !ok {
		resources["/robots.txt"] = ProxyResource{
			Path:         "/robots.txt",
			ResourcePath: "/web/robots.txt",
		}
	}
	if _, ok := resources["/sitemap.xml"]; !ok {
		resources["/sitemap.xml"] = ProxyResource{
			Path:         "/sitemap.xml",
			ResourcePath: "/web/sitemap.xml",
		}
	}
	if _, ok := resources["/ads.txt"]; !ok {
		resources["/ads.txt"] = ProxyResource{
			Path:         "/ads.txt",
			ResourcePath: "/web/ads.txt",
		}
	}

	h.proxyResources = resources
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(h.init)

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", h.etag)

	etag := r.Header.Get("If-None-Match")
	if etag == h.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	path := r.URL.Path

	fileHandler, isServingStaticResources := h.Resources.(http.Handler)
	if isServingStaticResources && strings.HasPrefix(path, "/web/") {
		fileHandler.ServeHTTP(w, r)
		return
	}

	switch path {
	case "/goapp.js":
		path = "/app.js"

	case "/manifest.json":
		path = "/manifest.webmanifest"

	case "/app.wasm", "/goapp.wasm":
		if isServingStaticResources {
			r2 := *r
			r2.URL.Path = h.Resources.AppWASM()
			fileHandler.ServeHTTP(w, &r2)
			return
		}

		w.WriteHeader(http.StatusNotFound)
		return

	}

	if res, ok := h.getResource(path); ok && !res.IsExpired() {
		h.serveResource(w, res)
		return
	}

	if proxyResource, ok := h.proxyResources[path]; ok {
		h.serveProxyResource(proxyResource, w, r)
		return
	}

	h.servePage(w, r)
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", strconv.Itoa(h.page.Len()))
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write(h.page.Bytes())
}

func (h *Handler) serveResource(w http.ResponseWriter, r httpResource) {
	w.Header().Set("Content-Length", strconv.Itoa(r.Len()))
	w.Header().Set("Content-Type", r.ContentType)
	w.WriteHeader(http.StatusOK)
	w.Write(r.Body)
}

func (h *Handler) serveProxyResource(resource ProxyResource, w http.ResponseWriter, r *http.Request) {
	var u string
	if _, ok := h.Resources.(http.Handler); ok {
		u = "http://" + r.Host + resource.ResourcePath
	} else {
		u = h.Resources.StaticResources() + resource.ResourcePath
	}

	res, err := http.Get(u)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		Log("%s", errors.New("getting proxy static resource failed").
			Tag("url", u).
			Tag("proxy-path", resource.Path).
			Tag("static-resource-path", resource.ResourcePath).
			Wrap(err),
		)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		Log("%s", errors.New("reading proxy static resource failed").
			Tag("url", u).
			Tag("proxy-path", resource.Path).
			Tag("static-resource-path", resource.ResourcePath).
			Wrap(err),
		)
		return
	}

	httpRes := httpResource{
		Path:        resource.Path,
		ContentType: res.Header.Get("Content-Type"),
		Body:        body,
	}
	h.setResource(resource.Path, httpRes)
	h.serveResource(w, httpRes)
}

func (h *Handler) appResource(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	path = h.Resources.AppResources() + path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if path != "/" && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}

	return path
}

func (h *Handler) staticResource(path string) string {
	if isRemoteLocation(path) {
		return path
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return h.Resources.StaticResources() + path
}

// Icon describes a square image that is used in various places such as
// application icon, favicon or loading icon.
type Icon struct {
	// The path or url to a square image/png file. It must have a side of 192px.
	//
	// Path is relative to the root directory.
	Default string

	// The path or url to larger square image/png file. It must have a side of
	// 512px.
	//
	// Path is relative to the root directory.
	Large string

	// The path or url to a square image/png file that is used for IOS/IPadOS
	// home screen icon. It must have a side of 192px.
	//
	// Path is relative to the root directory.
	//
	// DEFAULT: Icon.Default
	AppleTouch string
}

// Environment describes the environment variables to pass to the progressive
// web app.
type Environment map[string]string

func normalizeFilePath(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(path, "/", `\`)
	}
	return path
}

func isRemoteLocation(path string) bool {
	u, _ := url.Parse(path)
	return u.Scheme != ""
}

type proxyResourceCache struct {
	Body        []byte
	ContentType string
}

type httpResource struct {
	Path        string
	ContentType string
	Body        []byte
	ExpireAt    time.Time
}

func (r httpResource) Len() int {
	return len(r.Body)
}

func (r httpResource) IsExpired() bool {
	return r.ExpireAt != time.Time{} && r.ExpireAt.Before(time.Now())
}

// PageInfo contains the page info that is modifiable when a page is pre
// rendered.
type PageInfo struct {
	// The page authors.
	Author string

	// The page description.
	Description string

	// The page keywords.
	Keywords []string

	// The page title.
	Title string

	url *url.URL
}

// URL return the page URL.
func (i *PageInfo) URL() *url.URL {
	return i.url
}
