package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
)

type multipartView struct {
	MediaID, UploadID            uuid.UUID
	State, MIMEType              string
	SourceSHA256                 *string
	PartSize, ByteCount, Version int64
	Parts                        []struct {
		Number int
		URL    string
	}
	CompletedParts []completedUploadPart
}
type completedUploadPart struct {
	Number int    `json:"number"`
	ETag   string `json:"etag"`
}

func multipartInput(sub, key uuid.UUID, raw []byte) map[string]any {
	h := sha256.Sum256(raw)
	return map[string]any{"clientSubmissionId": sub, "clientUploadId": key, "sourceSha256": media.ETag(h[:]), "mimeType": "image/png", "byteCount": len(raw), "purpose": "REPORT"}
}
func multipartStatus(t *testing.T, c client, mid uuid.UUID) multipartView {
	t.Helper()
	r := c.request("GET", "media/"+mid.String()+"/upload", nil, 0, "")
	mustStatus(t, r, 200)
	return parsed[multipartView](t, r)
}
func multipartPut(c client, address string, raw []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", strings.Replace(address, "/api/", "/v1/", 1), bytes.NewReader(raw))
	r.AddCookie(c.cookie)
	r.Header.Set("X-JanSetu-CSRF", "1")
	w := httptest.NewRecorder()
	c.app.Handler().ServeHTTP(w, r)
	return w
}
func cleanupMultipart(t *testing.T, c client, mid uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		u := multipartStatus(t, c, mid)
		mustStatus(t, c.request("DELETE", "media/"+mid.String()+"/upload", nil, u.Version, ""), 204)
		if multipartStatus(t, c, mid).State != "ABORTED" {
			t.Fatal("removed upload remained usable")
		}
		var liveTokens int
		if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM infra.upload_part WHERE upload_id=$1 AND token_hash IS NOT NULL`, u.UploadID).Scan(&liveTokens); err != nil || liveTokens != 0 {
			t.Fatal("aborted part capability retained", err, liveTokens)
		}
		for n := 1; n <= media.MaxParts; n++ {
			if f, err := c.app.Files.Open(media.PartKey(u.UploadID, n)); err == nil {
				f.Close()
				t.Fatal("aborted part bytes retained")
			}
		}
	})
}

func TestMultipartAllocationRecoveryPartsIntegrityAndApproval(t *testing.T) {
	a := testApp(t)
	c, other := login(t, a, 0), login(t, a, 1)
	notice, err := os.ReadFile("../media/testdata/notice.png")
	if err != nil {
		t.Fatal(err)
	}
	// Decodable fictional image with padding exercises the two-part path. The
	// approved derivative removes the padding, while the source hash covers it.
	raw := append(notice, make([]byte, 3<<20)...)
	body := multipartInput(uuid.New(), uuid.New(), raw)
	var responses [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) { defer wg.Done(); responses[i] = c.request("POST", "media/uploads", body, 0, "") }(i)
	}
	wg.Wait()
	for _, r := range responses {
		mustStatus(t, r, 201)
	}
	u := parsed[multipartView](t, responses[0])
	u2 := parsed[multipartView](t, responses[1])
	if u.MediaID != u2.MediaID || u.UploadID != u2.UploadID {
		t.Fatal("concurrent allocation duplicated photo")
	}
	cleanupMultipart(t, c, u.MediaID)
	if u.PartSize != media.PartBytes || u.ByteCount != int64(len(raw)) || u.SourceSHA256 == nil {
		t.Fatal(u)
	}
	changed := multipartInput(body["clientSubmissionId"].(uuid.UUID), body["clientUploadId"].(uuid.UUID), append([]byte{}, raw...))
	changed["sourceSha256"] = strings.Repeat("0", 64)
	mustStatus(t, c.request("POST", "media/uploads", changed, 0, ""), 409)
	foreign := other.request("POST", "media/uploads", body, 0, "")
	mustStatus(t, foreign, 201)
	fu := parsed[multipartView](t, foreign)
	cleanupMultipart(t, other, fu.MediaID)
	if fu.MediaID == u.MediaID {
		t.Fatal("allocation identity escaped owner scope")
	}
	mustStatus(t, other.request("GET", "media/"+u.MediaID.String()+"/upload", nil, 0, ""), 404)
	u = multipartStatus(t, c, u.MediaID)
	path := "media/" + u.MediaID.String()
	mustStatus(t, c.request("POST", path+"/upload-parts", map[string]any{"partNumbers": []int{1, 1}}, u.Version, ""), 422)
	mustStatus(t, c.request("POST", path+"/upload-parts", map[string]any{"partNumbers": []int{3}}, u.Version, ""), 422)
	r := c.request("POST", path+"/upload-parts", map[string]any{"partNumbers": []int{1, 2}}, u.Version, "")
	mustStatus(t, r, 200)
	renewed := parsed[multipartView](t, r)
	mustStatus(t, c.request("POST", path+"/upload-parts", map[string]any{"partNumbers": []int{1}}, u.Version, ""), 412)
	p1url, p2url := renewed.Parts[0].URL, renewed.Parts[1].URL
	mustStatus(t, multipartPut(other, p1url, raw[:media.PartBytes]), 404)
	mustStatus(t, multipartPut(c, strings.Replace(p1url, "/parts/1", "/parts/2", 1), raw[media.PartBytes:]), 403)
	mustStatus(t, multipartPut(c, p1url, raw[:100]), 422)
	put := multipartPut(c, p1url, raw[:media.PartBytes])
	mustStatus(t, put, 200)
	p1 := parsed[completedUploadPart](t, put)
	status := multipartStatus(t, c, u.MediaID)
	if len(status.CompletedParts) != 1 || len(status.Parts) != 0 {
		t.Fatal("status leaked capability or missed committed part", status)
	}
	mustStatus(t, multipartPut(c, p1url, raw[:media.PartBytes]), 200)
	if multipartStatus(t, c, u.MediaID).Version != status.Version {
		t.Fatal("identical part retry changed version")
	}
	different := append([]byte{}, raw[:media.PartBytes]...)
	different[0]++
	mustStatus(t, multipartPut(c, p1url, different), 409)
	rotated := c.request("POST", path+"/upload-parts", map[string]any{"partNumbers": []int{2}}, status.Version, "")
	mustStatus(t, rotated, 200)
	mustStatus(t, multipartPut(c, p2url, raw[media.PartBytes:]), 403)
	p2url = parsed[multipartView](t, rotated).Parts[0].URL
	put = multipartPut(c, p2url, raw[media.PartBytes:])
	mustStatus(t, put, 200)
	p2 := parsed[completedUploadPart](t, put)
	mustStatus(t, c.request("POST", path+"/complete", map[string]any{"parts": []completedUploadPart{p1}}, 0, ""), 422)
	mustStatus(t, c.request("POST", path+"/complete", map[string]any{"parts": []completedUploadPart{p1, p1}}, 0, ""), 422)
	// A committed DB record cannot conceal a missing part on disk. Identical
	// retry repairs the file and completion verifies every part plus the whole.
	if err = a.Files.Remove(media.PartKey(u.UploadID, 1)); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c.request("POST", path+"/complete", map[string]any{"parts": []completedUploadPart{p2, p1}}, 0, ""), 422)
	mustStatus(t, multipartPut(c, p1url, raw[:media.PartBytes]), 200)
	mustStatus(t, c.request("POST", path+"/complete", map[string]any{"parts": []completedUploadPart{p2, p1}}, 0, ""), 202)
	var stored []byte
	if err = integrationAdmin.QueryRow(context.Background(), `SELECT sha256 FROM social.media_asset WHERE id=$1`, u.MediaID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(raw)
	if !bytes.Equal(stored, want[:]) {
		t.Fatal("full source hash did not match")
	}
	var liveTokens int
	if err = integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM infra.upload_part WHERE upload_id=$1 AND token_hash IS NOT NULL`, u.UploadID).Scan(&liveTokens); err != nil || liveTokens != 0 {
		t.Fatal("closed part capability retained", err, liveTokens)
	}
	runMedia(t)
	runMedia(t)
	mustStatus(t, c.request("GET", path+"/content", nil, 0, ""), 200)
	for n := 1; n <= 2; n++ {
		if f, e := a.Files.Open(media.PartKey(u.UploadID, n)); e == nil {
			f.Close()
			t.Fatal("approved upload retained part bytes")
		}
	}
	// Completed retries work after cleanup, including a lost allocation ACK.
	mustStatus(t, c.request("POST", path+"/complete", map[string]any{"parts": []completedUploadPart{p1, p2}}, 0, ""), 202)
	r = c.request("POST", "media/uploads", body, 0, "")
	mustStatus(t, r, 201)
	recovered := parsed[multipartView](t, r)
	if recovered.MediaID != u.MediaID || recovered.State != "COMPLETE" || len(recovered.Parts) != 0 {
		t.Fatal(recovered)
	}
	for _, role := range []string{"js_auth", "js_social", "js_ops", "js_publication", "js_worker"} {
		var canRead bool
		if err = integrationAdmin.QueryRow(context.Background(), `SELECT has_table_privilege($1,'infra.upload_part','SELECT')`, role).Scan(&canRead); err != nil || canRead {
			t.Fatal("part metadata granted outside media service", role, err)
		}
	}
}

func TestMultipartWholeFingerprintMismatchAndExpiryCleanup(t *testing.T) {
	a := testApp(t)
	c := login(t, a, 1)
	for _, expire := range []bool{false, true} {
		raw := bytes.Repeat([]byte{42}, int(media.PartBytes)+17)
		body := multipartInput(uuid.New(), uuid.New(), raw)
		if !expire {
			body["sourceSha256"] = strings.Repeat("0", 64)
		}
		r := c.request("POST", "media/uploads", body, 0, "")
		mustStatus(t, r, 201)
		u := parsed[multipartView](t, r)
		cleanupMultipart(t, c, u.MediaID)
		parts := []completedUploadPart{}
		for _, p := range u.Parts {
			offset := int64(p.Number-1) * media.PartBytes
			put := multipartPut(c, p.URL, raw[offset:min(offset+media.PartBytes, int64(len(raw)))])
			mustStatus(t, put, 200)
			parts = append(parts, parsed[completedUploadPart](t, put))
		}
		if !expire {
			mustStatus(t, c.request("POST", "media/"+u.MediaID.String()+"/complete", map[string]any{"parts": parts}, 0, ""), 422)
			if multipartStatus(t, c, u.MediaID).State != "OPEN" {
				t.Fatal("bad fingerprint completed")
			}
			if f, e := a.Files.Open(media.OriginalKey(u.MediaID)); e == nil {
				f.Close()
				t.Fatal("bad assembly published")
			}
		} else {
			if _, e := integrationAdmin.Exec(context.Background(), `UPDATE infra.upload_session SET expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, u.UploadID); e != nil {
				t.Fatal(e)
			}
			runMedia(t)
			if multipartStatus(t, c, u.MediaID).State != "EXPIRED" {
				t.Fatal("expired session remained open")
			}
			var tokens int
			if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM infra.upload_part WHERE upload_id=$1 AND token_hash IS NOT NULL`, u.UploadID).Scan(&tokens); err != nil || tokens != 0 {
				t.Fatal("expired part capability retained", err, tokens)
			}
			for n := 1; n <= 2; n++ {
				if f, e := a.Files.Open(media.PartKey(u.UploadID, n)); e == nil {
					f.Close()
					t.Fatal("expired part retained")
				}
			}
		}
	}
}

func TestMultipartAllocationRetryAtReportQuota(t *testing.T) {
	c := login(t, testApp(t), 0)
	sub := uuid.New()
	raw := []byte("small test bytes")
	var first map[string]any
	var mid uuid.UUID
	for i := 0; i < 4; i++ {
		body := multipartInput(sub, uuid.New(), raw)
		r := c.request("POST", "media/uploads", body, 0, "")
		mustStatus(t, r, 201)
		u := parsed[multipartView](t, r)
		cleanupMultipart(t, c, u.MediaID)
		if i == 0 {
			first = body
			mid = u.MediaID
		}
	}
	mustStatus(t, c.request("POST", "media/uploads", multipartInput(sub, uuid.New(), raw), 0, ""), 422)
	r := c.request("POST", "media/uploads", first, 0, "")
	mustStatus(t, r, 201)
	if parsed[multipartView](t, r).MediaID != mid {
		t.Fatal("quota retry duplicated allocation")
	}
	delete(first, "sourceSha256")
	mustStatus(t, c.request("POST", "media/uploads", first, 0, ""), 422)
}
