package sc3d

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestViewerHandlerServesConfigAndEmbeddedPage(t *testing.T) {
	handler, err := newViewerHandler("abc123", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/config.json", nil)
	configRecorder := httptest.NewRecorder()
	handler.ServeHTTP(configRecorder, configRequest)
	if configRecorder.Code != http.StatusOK {
		t.Fatalf("config status = %d", configRecorder.Code)
	}
	var config configResponse
	if err := json.NewDecoder(configRecorder.Body).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config.Fingerprint != "abc123" || !strings.HasSuffix(config.BaseURL, "/abc123") {
		t.Fatalf("unexpected config: %+v", config)
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "SC3D Viewer") {
		t.Fatalf("viewer page status = %d", pageResponse.Code)
	}
	for _, expected := range []string{"landing-scale", "landing-allow-yaw", "export-glb", "export-skin-json", "webp-loop"} {
		if !strings.Contains(pageResponse.Body.String(), expected) {
			t.Errorf("viewer page is missing %q", expected)
		}
	}

	scriptRequest := httptest.NewRequest(http.MethodGet, "/viewer.js", nil)
	scriptResponse := httptest.NewRecorder()
	handler.ServeHTTP(scriptResponse, scriptRequest)
	if scriptResponse.Code != http.StatusOK {
		t.Fatalf("viewer script status = %d", scriptResponse.Code)
	}
	for _, expected := range []string{
		"GLTFExporter",
		"GLTFLoader",
		"buildExportAnimationClip",
		"compactVertexStream",
		"normalizedSkinAttributes",
		`new THREE.Scene()`,
		`skeletonRoot.name = "skeleton_root"`,
		"jointIndexOffset",
		"validateLandingGLB",
		"decodeNormalizedWeightVector",
		"resolveAnimationLocalTransform",
		"resolveAliasedRotationTransform",
		"resolveAnimationSourceName",
		"requiresAnimationGlobalRemap",
		"isPoseAnimationAsset",
		"webPAnimationOptions",
		"schemaVersion: 1",
		`model: "model.glb"`,
		"model.glb",
		"skin.json",
	} {
		if !strings.Contains(scriptResponse.Body.String(), expected) {
			t.Errorf("viewer script is missing %q", expected)
		}
	}

	webPOptionsRequest := httptest.NewRequest(http.MethodGet, "/webp-options.mjs", nil)
	webPOptionsResponse := httptest.NewRecorder()
	handler.ServeHTTP(webPOptionsResponse, webPOptionsRequest)
	if webPOptionsResponse.Code != http.StatusOK {
		t.Fatalf("webp options status = %d", webPOptionsResponse.Code)
	}
	for _, expected := range []string{"loop ? 0 : 1", "options[4]", "options[5]"} {
		if !strings.Contains(webPOptionsResponse.Body.String(), expected) {
			t.Errorf("webp options helper is missing %q", expected)
		}
	}

	poseCandidatesRequest := httptest.NewRequest(http.MethodGet, "/pose-candidates.mjs", nil)
	poseCandidatesResponse := httptest.NewRecorder()
	handler.ServeHTTP(poseCandidatesResponse, poseCandidatesRequest)
	if poseCandidatesResponse.Code != http.StatusOK {
		t.Fatalf("pose candidates status = %d", poseCandidatesResponse.Code)
	}
	for _, expected := range []string{`asset.endsWith(".glb")`, `!asset.includes("_geo")`, `!asset.includes(".ingame.")`} {
		if !strings.Contains(poseCandidatesResponse.Body.String(), expected) {
			t.Errorf("pose candidates helper is missing %q", expected)
		}
	}
	if strings.Contains(scriptResponse.Body.String(), "poster.webp") {
		t.Error("viewer script unexpectedly includes a per-skin poster export")
	}

	weightCodecRequest := httptest.NewRequest(http.MethodGet, "/weight-codec.mjs", nil)
	weightCodecResponse := httptest.NewRecorder()
	handler.ServeHTTP(weightCodecResponse, weightCodecRequest)
	if weightCodecResponse.Code != http.StatusOK {
		t.Fatalf("weight codec status = %d", weightCodecResponse.Code)
	}
	for _, expected := range []string{"NORMALIZED_WEIGHT_DENOMINATOR = 4095", "packed >>> 21", "packed >>> 10"} {
		if !strings.Contains(weightCodecResponse.Body.String(), expected) {
			t.Errorf("weight codec is missing %q", expected)
		}
	}

	animationCodecRequest := httptest.NewRequest(http.MethodGet, "/animation-codec.mjs", nil)
	animationCodecResponse := httptest.NewRecorder()
	handler.ServeHTTP(animationCodecResponse, animationCodecRequest)
	if animationCodecResponse.Code != http.StatusOK {
		t.Fatalf("animation codec status = %d", animationCodecResponse.Code)
	}
	for _, expected := range []string{"readContinuousPackedBases", "decodeContinuousPackedAnimation", "resolveAnimationLocalTransform", "resolveAliasedRotationTransform", "resolveAnimationSourceName", "requiresAnimationGlobalRemap"} {
		if !strings.Contains(animationCodecResponse.Body.String(), expected) {
			t.Errorf("animation codec is missing %q", expected)
		}
	}
}

func TestViewerHandlerRejectsInvalidRemotePaths(t *testing.T) {
	handler, err := newViewerHandler("abc123", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/remote/", "/remote/%2e%2e/secret", "/texture/not-a-texture.png"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusBadRequest)
		}
	}
}
