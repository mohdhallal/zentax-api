package documents_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

// DocumentsSuite proves the documents module end to end against live Postgres
// and the filesystem storage adapter (ADR-0022): multipart upload → view →
// lists → byte-identical download with the download headers → versioning →
// metadata → repository filters + paging → RBAC + tenancy → upload rules
// (413 / 415 / 400) → soft delete → the ADR-0018 freeze after a real
// submit / approve → PII-free audit entries.
type DocumentsSuite struct {
	acceptance.Suite
}

func TestDocumentsSuite(t *testing.T) {
	suite.Run(t, new(DocumentsSuite))
}

type docView struct {
	ID             string  `json:"id"`
	WorkflowID     string  `json:"workflowId"`
	TaskInstanceID *string `json:"taskInstanceId"`
	Category       string  `json:"category"`
	DocumentType   string  `json:"documentType"`
	Label          *string `json:"label"`
	Notes          *string `json:"notes"`
	FileName       string  `json:"fileName"`
	FileSize       int64   `json:"fileSize"`
	MimeType       string  `json:"mimeType"`
	SHA256         string  `json:"sha256"`
	Version        int     `json:"version"`
	VersionID      string  `json:"versionId"`
	UploadedBy     *string `json:"uploadedBy"`
	UploadedByName *string `json:"uploadedByName"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	WorkflowName   *string `json:"workflowName"`
	EntityID       *string `json:"entityId"`
	EntityName     *string `json:"entityName"`
	FinancialYear  *string `json:"financialYear"`
}

type versionView struct {
	ID             string  `json:"id"`
	DocumentID     string  `json:"documentId"`
	Version        int     `json:"version"`
	FileName       string  `json:"fileName"`
	FileSize       int64   `json:"fileSize"`
	MimeType       string  `json:"mimeType"`
	SHA256         string  `json:"sha256"`
	UploadedBy     *string `json:"uploadedBy"`
	UploadedByName *string `json:"uploadedByName"`
	CreatedAt      string  `json:"createdAt"`
}

type seeded struct {
	EntityID   string
	WorkflowID string
	Instances  []string
}

var pdfV1 = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n")
var pdfV2 = []byte("%PDF-1.7\n1 0 obj << /Type /Catalog /Version /1.7 >> endobj\n%%EOF\n")

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// seed builds entity → obligation type → recurring workflow (financialYear)
// → an approval-required task template → start, and returns the ids.
func (s *DocumentsSuite) seed(tenant, entityName, wfName, year, obCode string, periods []string) seeded {
	post := func(path string, body any) *acceptance.TestResponse {
		return s.As(tenant).POST(s.T(), path, body)
	}
	var entity, obType, wf struct {
		ID string `json:"id"`
	}
	r := post("/entities", map[string]any{
		"name": entityName, "country": "Germany", "financialYearEnd": "12-31", "fiscalCalendarPattern": "standard",
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &entity)

	r = post("/obligation-types", map[string]any{"name": "VAT " + obCode, "code": obCode, "template": "VAT"})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &obType)

	r = post("/workflows", map[string]any{
		"name": wfName, "workflowCategory": "recurring",
		"entityId": entity.ID, "obligationTypeId": obType.ID,
		"periodicity": "monthly", "financialYear": year, "selectedPeriods": periods,
		"dueDateRule": map[string]any{"reference": "period_end", "offsetUnit": "days", "offsetValue": 15, "offsetDirection": "after"},
	})
	r.AssertStatus(s.T(), http.StatusCreated)
	r.DecodeData(s.T(), &wf)

	post("/workflow-tasks", map[string]any{
		"workflowId": wf.ID, "name": "Prepare", "taskType": "preparation", "approvalRequired": true,
		"dueDateReference": "filing_deadline", "dueDateOffsetValue": 5, "dueDateOffsetUnit": "days", "dueDateOffsetDirection": "before",
	}).AssertStatus(s.T(), http.StatusCreated)

	s.As(tenant).POST(s.T(), "/workflows/"+wf.ID+"/start", nil).AssertStatus(s.T(), http.StatusCreated)

	var list []struct {
		ID string `json:"id"`
	}
	s.As(tenant).GET(s.T(), "/task-instances?workflowId="+wf.ID).DecodeData(s.T(), &list)
	out := seeded{EntityID: entity.ID, WorkflowID: wf.ID}
	for _, ti := range list {
		out.Instances = append(out.Instances, ti.ID)
	}
	s.Require().NotEmpty(out.Instances)
	return out
}

func (s *DocumentsSuite) upload(rb *acceptance.RequestBuilder, wfID string, fields map[string]string, file acceptance.MultipartFile) *acceptance.TestResponse {
	return rb.POSTMultipart(s.T(), "/workflows/"+wfID+"/documents", fields, file)
}

func (s *DocumentsSuite) mustUpload(tenant, wfID string, fields map[string]string, file acceptance.MultipartFile) docView {
	r := s.upload(s.As(tenant), wfID, fields, file)
	r.AssertStatus(s.T(), http.StatusCreated)
	var v docView
	r.DecodeData(s.T(), &v)
	return v
}

func pdf(name string, content []byte) acceptance.MultipartFile {
	return acceptance.MultipartFile{FileName: name, ContentType: "application/pdf", Content: content}
}

func (s *DocumentsSuite) ids(views []docView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.ID)
	}
	return out
}

func (s *DocumentsSuite) TestUploadListDownloadVersioningAndMetadata() {
	tenant := s.InsertTenant("doc-a", "Docs A").String()
	sd := s.seed(tenant, "Acme GmbH", "Monthly VAT 2025", "2025", "VAT-A", []string{"M1"})
	ti := sd.Instances[0]

	// Upload v1 attached to the instance.
	v1 := s.mustUpload(tenant, sd.WorkflowID, map[string]string{
		"documentType": "draft_return", "label": "Q1 draft", "notes": "first cut", "taskInstanceId": ti,
	}, pdf("VAT return Q1 (final).pdf", pdfV1))

	s.Require().Equal(sd.WorkflowID, v1.WorkflowID)
	s.Require().NotNil(v1.TaskInstanceID)
	s.Require().Equal(ti, *v1.TaskInstanceID)
	s.Require().Equal("compliance", v1.Category, "default category")
	s.Require().Equal("draft_return", v1.DocumentType)
	s.Require().Equal("Q1 draft", *v1.Label)
	s.Require().Equal("first cut", *v1.Notes)
	s.Require().Equal("VAT return Q1 (final).pdf", v1.FileName)
	s.Require().Equal(int64(len(pdfV1)), v1.FileSize)
	s.Require().Equal("application/pdf", v1.MimeType)
	s.Require().Equal(sha(pdfV1), v1.SHA256)
	s.Require().Equal(1, v1.Version)
	s.Require().NotEmpty(v1.VersionID)
	s.Require().NotNil(v1.UploadedBy)
	s.Require().NotNil(v1.UploadedByName)
	s.Require().Equal("Test User", *v1.UploadedByName)
	s.Require().Equal("Monthly VAT 2025", *v1.WorkflowName)
	s.Require().Equal(sd.EntityID, *v1.EntityID)
	s.Require().Equal("Acme GmbH", *v1.EntityName)
	s.Require().Equal("2025", *v1.FinancialYear)
	s.Require().Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`, v1.CreatedAt)

	// Listed on the workflow and on the instance; GET by id.
	var onWF, onTI []docView
	s.As(tenant).GET(s.T(), "/workflows/"+sd.WorkflowID+"/documents").DecodeData(s.T(), &onWF)
	s.As(tenant).GET(s.T(), "/task-instances/"+ti+"/documents").DecodeData(s.T(), &onTI)
	s.Require().Equal([]string{v1.ID}, s.ids(onWF))
	s.Require().Equal([]string{v1.ID}, s.ids(onTI))
	var got docView
	s.As(tenant).GET(s.T(), "/documents/"+v1.ID).DecodeData(s.T(), &got)
	s.Require().Equal(v1, got)

	// Download: byte-identical + the download headers.
	dl := s.As(tenant).GET(s.T(), "/documents/"+v1.ID+"/download")
	dl.AssertStatus(s.T(), http.StatusOK)
	s.Require().True(bytes.Equal(pdfV1, dl.Bytes()), "download bytes differ")
	s.Require().Equal("application/pdf", dl.Header.Get("Content-Type"))
	s.Require().Equal(strconv.Itoa(len(pdfV1)), dl.Header.Get("Content-Length"))
	s.Require().Equal(`attachment; filename="VAT return Q1 (final).pdf"; filename*=UTF-8''VAT%20return%20Q1%20%28final%29.pdf`,
		dl.Header.Get("Content-Disposition"))
	s.Require().Equal("private, no-store", dl.Header.Get("Cache-Control"))
	s.Require().Equal("nosniff", dl.Header.Get("X-Content-Type-Options"))
	s.Require().NotEmpty(dl.Header.Get("X-Request-Id"))

	// New version (with a relabel): version 2 is the latest.
	r := s.As(tenant).POSTMultipart(s.T(), "/documents/"+v1.ID+"/versions",
		map[string]string{"label": "Q1 final"}, pdf("rapport été.pdf", pdfV2))
	r.AssertStatus(s.T(), http.StatusCreated)
	var v2 docView
	r.DecodeData(s.T(), &v2)
	s.Require().Equal(v1.ID, v2.ID)
	s.Require().Equal(2, v2.Version)
	s.Require().NotEqual(v1.VersionID, v2.VersionID)
	s.Require().Equal("rapport été.pdf", v2.FileName)
	s.Require().Equal(sha(pdfV2), v2.SHA256)
	s.Require().Equal(int64(len(pdfV2)), v2.FileSize)
	s.Require().Equal("Q1 final", *v2.Label)
	s.Require().Equal("first cut", *v2.Notes, "notes untouched by a version add")

	var versions []versionView
	s.As(tenant).GET(s.T(), "/documents/"+v1.ID+"/versions").DecodeData(s.T(), &versions)
	s.Require().Len(versions, 2)
	s.Require().Equal(2, versions[0].Version)
	s.Require().Equal(v2.VersionID, versions[0].ID)
	s.Require().Equal(1, versions[1].Version)
	s.Require().Equal(v1.VersionID, versions[1].ID)
	s.Require().Equal(v1.ID, versions[1].DocumentID)
	s.Require().Equal("VAT return Q1 (final).pdf", versions[1].FileName)
	s.Require().Equal("Test User", *versions[0].UploadedByName)

	// Latest download is v2 (UTF-8 name → ASCII fallback + RFC 5987 form);
	// v1 stays downloadable by version id.
	dl2 := s.As(tenant).GET(s.T(), "/documents/"+v1.ID+"/download")
	dl2.AssertStatus(s.T(), http.StatusOK)
	s.Require().True(bytes.Equal(pdfV2, dl2.Bytes()))
	s.Require().Equal(`attachment; filename="rapport _t_.pdf"; filename*=UTF-8''rapport%20%C3%A9t%C3%A9.pdf`,
		dl2.Header.Get("Content-Disposition"))
	dl1 := s.As(tenant).GET(s.T(), "/documents/"+v1.ID+"/versions/"+v1.VersionID+"/download")
	dl1.AssertStatus(s.T(), http.StatusOK)
	s.Require().True(bytes.Equal(pdfV1, dl1.Bytes()))
	s.As(tenant).GET(s.T(), "/documents/"+v1.ID+"/versions/"+uuid.NewString()+"/download").
		AssertStatus(s.T(), http.StatusNotFound)

	// Lists show the latest version.
	s.As(tenant).GET(s.T(), "/workflows/"+sd.WorkflowID+"/documents").DecodeData(s.T(), &onWF)
	s.Require().Len(onWF, 1)
	s.Require().Equal(2, onWF[0].Version)

	// PUT: only the given fields change; omitted stay; "" clears.
	var upd docView
	ru := s.As(tenant).PUT(s.T(), "/documents/"+v1.ID, map[string]any{"documentType": "final_return", "category": "project"})
	ru.AssertStatus(s.T(), http.StatusOK)
	ru.DecodeData(s.T(), &upd)
	s.Require().Equal("final_return", upd.DocumentType)
	s.Require().Equal("project", upd.Category)
	s.Require().Equal("Q1 final", *upd.Label)
	s.Require().Equal("first cut", *upd.Notes)
	s.Require().Equal(2, upd.Version)
	ru = s.As(tenant).PUT(s.T(), "/documents/"+v1.ID, map[string]any{"notes": ""})
	ru.AssertStatus(s.T(), http.StatusOK)
	ru.DecodeData(s.T(), &upd)
	s.Require().Nil(upd.Notes)
	s.Require().Equal("Q1 final", *upd.Label)
	s.As(tenant).PUT(s.T(), "/documents/"+v1.ID, map[string]any{"documentType": "invoice"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).PUT(s.T(), "/documents/"+v1.ID, map[string]any{"bogus": 1}).
		AssertStatus(s.T(), http.StatusBadRequest)

	// Audit: created / version_added / updated, PII-free (no file names, labels, notes).
	var audit []struct {
		Action     string         `json:"action"`
		ResourceID string         `json:"resourceId"`
		Details    map[string]any `json:"details"`
	}
	s.As(tenant).GET(s.T(), "/audit-log?resourceId="+v1.ID).DecodeData(s.T(), &audit)
	actions := map[string]map[string]any{}
	for _, a := range audit {
		actions[a.Action] = a.Details
		for _, k := range []string{"fileName", "label", "notes", "name", "email"} {
			s.Require().NotContains(a.Details, k, "audit %s leaks %s", a.Action, k)
		}
	}
	s.Require().Contains(actions, "document.created")
	s.Require().Contains(actions, "document.version_added")
	s.Require().Contains(actions, "document.updated")
	s.Require().Equal("draft_return", actions["document.created"]["documentType"])
	s.Require().Equal("compliance", actions["document.created"]["category"])
	s.Require().EqualValues(1, actions["document.created"]["version"])
	s.Require().EqualValues(len(pdfV1), actions["document.created"]["fileSize"])
	s.Require().EqualValues(2, actions["document.version_added"]["version"])
	s.Require().EqualValues(len(pdfV2), actions["document.version_added"]["fileSize"])
	s.Require().Equal("final_return", actions["document.updated"]["documentType"])
}

func (s *DocumentsSuite) TestRepositoryFiltersAndPaging() {
	tenant := s.InsertTenant("doc-b", "Docs B").String()
	a := s.seed(tenant, "Alpha AG", "VAT 2024", "2024", "VAT-B1", []string{"M1"})
	b := s.seed(tenant, "Beta BV", "VAT 2025", "2025", "VAT-B2", []string{"M1"})

	d1 := s.mustUpload(tenant, a.WorkflowID, map[string]string{"documentType": "draft_return", "label": "alpha draft"},
		pdf("alpha-draft.pdf", pdfV1))
	d2 := s.mustUpload(tenant, a.WorkflowID, map[string]string{"documentType": "workings", "notes": "100% reconciled_ok", "taskInstanceId": a.Instances[0]},
		acceptance.MultipartFile{FileName: "alpha workings.csv", ContentType: "text/csv", Content: []byte("a,b\n1,2\n")})
	d3 := s.mustUpload(tenant, b.WorkflowID, map[string]string{"documentType": "draft_return"},
		pdf("beta-draft.pdf", pdfV2))

	type page struct {
		Status     bool      `json:"status"`
		Data       []docView `json:"data"`
		Pagination struct {
			Total  int `json:"total"`
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		} `json:"pagination"`
	}
	list := func(query string) page {
		r := s.As(tenant).GET(s.T(), "/documents"+query)
		r.AssertStatus(s.T(), http.StatusOK)
		var p page
		s.Require().NoError(jsonUnmarshal(r.BodyString(), &p))
		return p
	}

	all := list("")
	s.Require().Equal(3, all.Pagination.Total)
	s.Require().Equal(100, all.Pagination.Limit)
	s.Require().Equal([]string{d3.ID, d2.ID, d1.ID}, s.ids(all.Data), "created_at desc")

	s.Require().Equal([]string{d2.ID, d1.ID}, s.ids(list("?entityId="+a.EntityID).Data))
	s.Require().Equal([]string{d3.ID}, s.ids(list("?year=2025").Data))
	s.Require().Equal([]string{d3.ID, d1.ID}, s.ids(list("?documentType=draft_return").Data))
	s.Require().Equal([]string{d2.ID}, s.ids(list("?workflowId="+a.WorkflowID+"&documentType=workings").Data))
	s.Require().Equal([]string{d2.ID}, s.ids(list("?taskInstanceId="+a.Instances[0]).Data))
	s.Require().Equal(3, list("?entityId=all&year=all&documentType=all&workflowId=&search=").Pagination.Total)

	// Search over file name / label / notes; LIKE metacharacters are literal.
	s.Require().Equal([]string{d2.ID, d1.ID}, s.ids(list("?search=alpha").Data))
	s.Require().Equal([]string{d1.ID}, s.ids(list("?search="+url.QueryEscape("alpha draft")).Data))
	s.Require().Equal([]string{d2.ID}, s.ids(list("?search="+url.QueryEscape("100% reconciled_ok")).Data))
	s.Require().Empty(list("?search="+url.QueryEscape("100% reconciledXok")).Data, "_ is not a wildcard")
	s.Require().Equal([]string{d2.ID}, s.ids(list("?search="+url.QueryEscape("%")).Data),
		"% is not a wildcard: only the note containing a literal % matches")
	s.Require().Equal([]string{d2.ID}, s.ids(list("?search="+url.QueryEscape("_")).Data),
		"_ is not a wildcard: only the note containing a literal _ matches")

	// Exact totals with paging.
	p := list("?limit=2&offset=0")
	s.Require().Len(p.Data, 2)
	s.Require().Equal(3, p.Pagination.Total)
	p = list("?limit=2&offset=2")
	s.Require().Len(p.Data, 1)
	s.Require().Equal(3, p.Pagination.Total)
	s.Require().Equal(d1.ID, p.Data[0].ID)
	s.As(tenant).GET(s.T(), "/documents?limit=501").AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).GET(s.T(), "/documents?entityId=not-a-uuid").AssertStatus(s.T(), http.StatusBadRequest)
}

func (s *DocumentsSuite) TestRBACAndTenancy() {
	tenant := s.InsertTenant("doc-c", "Docs C").String()
	other := s.InsertTenant("doc-c2", "Docs C2").String()
	sd := s.seed(tenant, "Gamma", "VAT", "2025", "VAT-C", []string{"M1"})
	ti := sd.Instances[0]
	doc := s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "other", "taskInstanceId": ti}, pdf("g.pdf", pdfV1))

	// Viewer reads but cannot write; preparer can upload; anonymous 401.
	s.upload(s.AsRole(tenant, "viewer"), sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("v.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "viewer").GET(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "viewer").GET(s.T(), "/documents/"+doc.ID+"/download").AssertStatus(s.T(), http.StatusOK)
	s.AsRole(tenant, "viewer").PUT(s.T(), "/documents/"+doc.ID, map[string]any{"label": "x"}).AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "viewer").DELETE(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusForbidden)
	s.AsRole(tenant, "reviewer").POSTMultipart(s.T(), "/documents/"+doc.ID+"/versions", nil, pdf("r.pdf", pdfV2)).
		AssertStatus(s.T(), http.StatusForbidden)
	s.upload(s.AsRole(tenant, "preparer"), sd.WorkflowID, map[string]string{"documentType": "workings"}, pdf("p.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusCreated)
	s.Client.External().GET(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusUnauthorized)
	s.AsUngranted(tenant).GET(s.T(), "/documents").AssertStatus(s.T(), http.StatusForbidden)

	// The other tenant sees nothing: every id-addressed route is a 404.
	o := s.As(other)
	o.GET(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusNotFound)
	o.GET(s.T(), "/documents/"+doc.ID+"/download").AssertStatus(s.T(), http.StatusNotFound)
	o.GET(s.T(), "/documents/"+doc.ID+"/versions").AssertStatus(s.T(), http.StatusNotFound)
	o.GET(s.T(), "/documents/"+doc.ID+"/versions/"+doc.VersionID+"/download").AssertStatus(s.T(), http.StatusNotFound)
	o.PUT(s.T(), "/documents/"+doc.ID, map[string]any{"label": "x"}).AssertStatus(s.T(), http.StatusNotFound)
	o.DELETE(s.T(), "/documents/"+doc.ID).AssertStatus(s.T(), http.StatusNotFound)
	o.POSTMultipart(s.T(), "/documents/"+doc.ID+"/versions", nil, pdf("o.pdf", pdfV2)).AssertStatus(s.T(), http.StatusNotFound)
	o.GET(s.T(), "/workflows/"+sd.WorkflowID+"/documents").AssertStatus(s.T(), http.StatusNotFound)
	o.GET(s.T(), "/task-instances/"+ti+"/documents").AssertStatus(s.T(), http.StatusNotFound)
	s.upload(o, sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("o.pdf", pdfV1)).AssertStatus(s.T(), http.StatusNotFound)
	var none []docView
	o.GET(s.T(), "/documents").DecodeData(s.T(), &none)
	s.Require().Empty(none)

	// The owning tenant still has both documents.
	var mine []docView
	s.As(tenant).GET(s.T(), "/documents").DecodeData(s.T(), &mine)
	s.Require().Len(mine, 2)
}

func (s *DocumentsSuite) TestUploadRules() {
	tenant := s.InsertTenant("doc-d", "Docs D").String()
	sd := s.seed(tenant, "Delta", "VAT", "2025", "VAT-D", []string{"M1"})
	sd2 := s.seed(tenant, "Delta 2", "VAT 2", "2025", "VAT-D2", []string{"M1"})

	// Oversize (declared size above the 2 MiB test cap) → 413.
	big := bytes.Repeat([]byte("x"), 2<<20+1)
	big[0], big[1], big[2], big[3], big[4] = '%', 'P', 'D', 'F', '-'
	r := s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("big.pdf", big))
	r.AssertStatus(s.T(), http.StatusRequestEntityTooLarge)
	r.AssertErrorCode(s.T(), "FILE_TOO_LARGE")

	// Disallowed declared type → 415; content/declared mismatch → 415.
	r = s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"},
		acceptance.MultipartFile{FileName: "x.html", ContentType: "text/html", Content: []byte("<html></html>")})
	r.AssertStatus(s.T(), http.StatusUnsupportedMediaType)
	r.AssertErrorCode(s.T(), "UNSUPPORTED_MEDIA_TYPE")
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, make([]byte, 32)...)
	r = s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("fake.pdf", png))
	r.AssertStatus(s.T(), http.StatusUnsupportedMediaType)
	// …but a PNG declared as PNG, and a docx (zip) declared as docx, are fine.
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"},
		acceptance.MultipartFile{FileName: "scan.png", ContentType: "image/png", Content: png}).AssertStatus(s.T(), http.StatusCreated)
	zip := append([]byte{'P', 'K', 3, 4}, make([]byte, 32)...)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"},
		acceptance.MultipartFile{FileName: "memo.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Content: zip}).
		AssertStatus(s.T(), http.StatusCreated)
	// No declared type → octet-stream, accepted for opaque bytes.
	v := s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "other"},
		acceptance.MultipartFile{FileName: "blob.bin", Content: []byte{0, 1, 2, 3, 0xff, 0xfe}})
	s.Require().Equal("application/octet-stream", v.MimeType)

	// 400s: missing file, unknown / missing documentType, bad category, foreign
	// task instance, non-multipart body, path-only file name.
	s.As(tenant).POSTMultipart(s.T(), "/workflows/"+sd.WorkflowID+"/documents", map[string]string{"documentType": "other"},
		acceptance.MultipartFile{Field: "attachment", FileName: "x.pdf", ContentType: "application/pdf", Content: pdfV1}).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "invoice"}, pdf("x.pdf", pdfV1)).AssertStatus(s.T(), http.StatusBadRequest)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{}, pdf("x.pdf", pdfV1)).AssertStatus(s.T(), http.StatusBadRequest)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other", "category": "misc"}, pdf("x.pdf", pdfV1)).AssertStatus(s.T(), http.StatusBadRequest)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other", "taskInstanceId": sd2.Instances[0]}, pdf("x.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other", "taskInstanceId": "nope"}, pdf("x.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusBadRequest)
	s.As(tenant).POST(s.T(), "/workflows/"+sd.WorkflowID+"/documents", map[string]any{"documentType": "other"}).
		AssertStatus(s.T(), http.StatusBadRequest)
	// (Go's multipart reader already reduces "dir/" to "dir"; a blank name is
	// what reaches the server as empty.)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("   ", pdfV1)).AssertStatus(s.T(), http.StatusBadRequest)
	// Unknown workflow → 404.
	s.upload(s.As(tenant), uuid.NewString(), map[string]string{"documentType": "other"}, pdf("x.pdf", pdfV1)).AssertStatus(s.T(), http.StatusNotFound)

	// The file name keeps only its base name.
	v = s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "other"}, pdf(`C:\Users\me\return.pdf`, pdfV1))
	s.Require().Equal("return.pdf", v.FileName)
}

func (s *DocumentsSuite) TestSoftDeleteAndApprovedFreeze() {
	tenant := s.InsertTenant("doc-e", "Docs E").String()
	sd := s.seed(tenant, "Epsilon", "VAT", "2025", "VAT-E", []string{"M1"})
	ti := sd.Instances[0]

	loose := s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("loose.pdf", pdfV1))
	attached := s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "supporting_docs", "taskInstanceId": ti}, pdf("evidence.pdf", pdfV1))

	// Soft delete: 204, then 404 everywhere and gone from every list; the
	// rows + blob are retained (ADR-0007).
	s.As(tenant).DELETE(s.T(), "/documents/"+loose.ID).AssertStatus(s.T(), http.StatusNoContent)
	s.As(tenant).GET(s.T(), "/documents/"+loose.ID).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).GET(s.T(), "/documents/"+loose.ID+"/download").AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).GET(s.T(), "/documents/"+loose.ID+"/versions").AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).PUT(s.T(), "/documents/"+loose.ID, map[string]any{"label": "x"}).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).POSTMultipart(s.T(), "/documents/"+loose.ID+"/versions", nil, pdf("x.pdf", pdfV2)).AssertStatus(s.T(), http.StatusNotFound)
	s.As(tenant).DELETE(s.T(), "/documents/"+loose.ID).AssertStatus(s.T(), http.StatusNotFound)
	var onWF, all []docView
	s.As(tenant).GET(s.T(), "/workflows/"+sd.WorkflowID+"/documents").DecodeData(s.T(), &onWF)
	s.As(tenant).GET(s.T(), "/documents").DecodeData(s.T(), &all)
	s.Require().Equal([]string{attached.ID}, s.ids(onWF))
	s.Require().Equal([]string{attached.ID}, s.ids(all))
	var retained struct {
		Deleted  bool `db:"deleted"`
		Versions int  `db:"versions"`
	}
	tx := s.DB.MustBegin()
	tx.MustExec(`SELECT set_config('app.tenant_id', $1, true)`, tenant)
	s.Require().NoError(tx.Get(&retained, `
		SELECT d.deleted_at IS NOT NULL AS deleted, (SELECT COUNT(*)::int FROM document_versions v WHERE v.document_id = d.id) AS versions
		FROM documents d WHERE d.id = $1`, loose.ID))
	s.Require().NoError(tx.Commit())
	s.Require().True(retained.Deleted)
	s.Require().Equal(1, retained.Versions)

	// Submitted for approval: the evidence under review is locked (409) until
	// the reviewer decides — new versions, deletion and new attachments alike;
	// reads still work.
	s.AsRole(tenant, "preparer").POST(s.T(), "/task-instances/"+ti+"/submit-for-approval", nil).AssertStatus(s.T(), http.StatusOK)
	pending := s.As(tenant).POSTMultipart(s.T(), "/documents/"+attached.ID+"/versions", nil, pdf("v2.pdf", pdfV2))
	pending.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Contains(pending.BodyString(), "awaiting approval")
	s.As(tenant).DELETE(s.T(), "/documents/"+attached.ID).AssertStatus(s.T(), http.StatusConflict)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other", "taskInstanceId": ti}, pdf("late.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusConflict)
	s.As(tenant).GET(s.T(), "/documents/"+attached.ID+"/download").AssertStatus(s.T(), http.StatusOK)

	// Approved through the real flow: frozen for good.
	s.AsRole(tenant, "reviewer").POST(s.T(), "/task-instances/"+ti+"/approve", nil).AssertStatus(s.T(), http.StatusOK)

	// The attached document is frozen (ADR-0018); reads still work.
	rc := s.As(tenant).POSTMultipart(s.T(), "/documents/"+attached.ID+"/versions", nil, pdf("v2.pdf", pdfV2))
	rc.AssertStatus(s.T(), http.StatusConflict)
	s.Require().Contains(rc.BodyString(), "documents of an approved task are immutable")
	s.As(tenant).PUT(s.T(), "/documents/"+attached.ID, map[string]any{"label": "x"}).AssertStatus(s.T(), http.StatusConflict)
	s.As(tenant).DELETE(s.T(), "/documents/"+attached.ID).AssertStatus(s.T(), http.StatusConflict)
	s.upload(s.As(tenant), sd.WorkflowID, map[string]string{"documentType": "other", "taskInstanceId": ti}, pdf("late.pdf", pdfV1)).
		AssertStatus(s.T(), http.StatusConflict)
	s.As(tenant).GET(s.T(), "/documents/"+attached.ID+"/download").AssertStatus(s.T(), http.StatusOK)
	var got docView
	s.As(tenant).GET(s.T(), "/documents/"+attached.ID).DecodeData(s.T(), &got)
	s.Require().Equal(1, got.Version)
	// A workflow-level document (no task instance) stays editable.
	free := s.mustUpload(tenant, sd.WorkflowID, map[string]string{"documentType": "other"}, pdf("free.pdf", pdfV1))
	s.As(tenant).PUT(s.T(), "/documents/"+free.ID, map[string]any{"label": "still editable"}).AssertStatus(s.T(), http.StatusOK)

	// Audit: deleted landed, PII-free.
	var audit []struct {
		Action  string         `json:"action"`
		Details map[string]any `json:"details"`
	}
	s.As(tenant).GET(s.T(), "/audit-log?resourceId="+loose.ID).DecodeData(s.T(), &audit)
	var sawDelete bool
	for _, a := range audit {
		if a.Action == "document.deleted" {
			sawDelete = true
			s.Require().Empty(a.Details)
		}
	}
	s.Require().True(sawDelete, "document.deleted audit entry")
}
