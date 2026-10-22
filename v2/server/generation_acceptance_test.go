package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This acceptance matrix proves prompt/reference/workflow wiring with a local
// fake generator. It deliberately never marks the aesthetic/manual gate passed.
func TestIsolatedGenerationAcceptanceMatrix(t *testing.T) {
	fixtures := []BrandForm{
		{"cn_name": "山序", "en_name": "SHAN XU", "industry": "茶饮", "products": "冷泡茶"},
		{"cn_name": "原点", "en_name": "ORIGIN", "industry": "科技 AI", "products": "数据分析工具"},
		{"cn_name": "麦时", "en_name": "MAI SHI", "industry": "烘焙", "products": "酸种面包"},
		{"cn_name": "木间", "en_name": "MU JIAN", "industry": "家居", "products": "实木家具"},
		{"cn_name": "微光", "en_name": "WEI GUANG", "industry": "美妆", "products": "香氛"},
	}
	type entry struct {
		Brand             string            `json:"brand"`
		Direction         LogoDirection     `json:"direction"`
		Prompts           map[string]string `json:"prompts"`
		ReferencePolicies map[string]string `json:"referencePolicies"`
		VisualReview      string            `json:"visualReview"`
	}
	rows := []entry{}
	for _, raw := range fixtures {
		raw["lang"] = "中英结合"
		raw["avoid"] = "禁止红色；禁止印章"
		raw["primary_color"] = "#246A73"
		raw["accent_colors"] = "#E6D4B2"
		raw["materials"] = "棉纸"
		raw["positioning"] = "城市年轻人"
		raw["logo_type"] = "抽象符号"
		f := normalizeForm(raw)
		freezeDesignConstraints(f, raw)
		brief := fallbackBrandBrief(f)
		dirs, err := buildLogoDirections(t.Context(), brief)
		if err != nil {
			t.Fatal(err)
		}
		if len(dirs) != 3 {
			t.Fatal("need three directions")
		}
		seen := map[string]bool{}
		for _, d := range dirs {
			key := d.Presentation.Format + "/" + d.Presentation.Composition
			if seen[key] {
				t.Fatalf("%s has duplicate presentation", raw["cn_name"])
			}
			seen[key] = true
			snap := buildLogoDirectionSnapshot(d)
			prompts := map[string]string{"direction": buildDirectionCardPrompt(brief, d), "formal_logo": buildFormalLogoPrompt(brief, d)}
			policies := map[string]string{}
			for _, spec := range itemSpecs(f) {
				if spec.Prompt != "" {
					prompts[spec.Kind] = buildItemPromptWithContext(f, spec, &snap, nil)
					policies[spec.Kind] = string(referencePolicyFor(spec.Kind))
				}
			}
			for stage, p := range prompts {
				for _, need := range []string{raw["en_name"], "#246A73", "禁止红色", "棉纸"} {
					if !strings.Contains(p, need) {
						t.Fatalf("%s/%s lost %s", d.ID, stage, need)
					}
				}
				if len([]rune(p)) > 2600 {
					t.Fatalf("prompt too long: %s %d", stage, len([]rune(p)))
				}
			}
			rows = append(rows, entry{raw["cn_name"], d, prompts, policies, "pending_real_images_and_human_review"})
		}
	}
	if len(rows) != 15 {
		t.Fatal("matrix incomplete")
	}
	if out := os.Getenv("ACCEPTANCE_OUTPUT_DIR"); out != "" {
		if !filepath.IsAbs(out) {
			t.Fatal("output must be absolute")
		}
		if err := os.MkdirAll(out, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(map[string]any{"mode": "isolated-prompts-and-mock-workflow", "cases": rows, "realImageVisualAcceptance": "pending"}, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "generation-matrix.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestIsolatedConstrainedGenerationLifecycle(t *testing.T) {
	j := commercialJob(t, "queued", "logo", 0)
	raw := constraintFixture()
	f := normalizeForm(raw)
	freezeDesignConstraints(f, raw)
	j.Form = f
	mustExec(t, "UPDATE jobs SET form=$2,brand_brief=NULL,logo_directions=NULL WHERE id=$1", j.ID, mustJSON(f))
	commercialWritePNG(t, j.ID, "direction_0.png", commercialPNG(t, 20))
	commercialGenerator(t, nil)
	if err := stageLogo(j); err != nil {
		t.Fatal(err)
	}
	mustExec(t, "UPDATE jobs SET logo_revision_instruction='只调整间距' WHERE id=$1", j.ID)
	if err := stageLogoRevision(j); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(contextBG(), "SELECT prompt FROM generation_records WHERE job_id=$1 AND status='succeeded' AND step IN ('formal_logo','logo_revision')", j.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		n++
		for _, need := range []string{"SHAN XU", "#1357AD", "禁止叶片"} {
			if !strings.Contains(p, need) {
				t.Errorf("persisted generation lost %s", need)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("generation records %d", n)
	}
}

func TestIsolatedFifteenDirectionDeliveriesAndDownloads(t *testing.T) {
	for _, industry := range []string{"茶饮", "科技 AI", "烘焙", "家居", "美妆"} {
		for chosen := 0; chosen < 3; chosen++ {
			t.Run(industry+string(rune('A'+chosen)), func(t *testing.T) {
				j := commercialJob(t, "running", "directions", chosen)
				raw := BrandForm{"cn_name": "隔离验收", "en_name": "ISOLATED", "industry": industry, "primary_color": "#246A73", "lang": "中英结合", "avoid": "禁止红色"}
				j.Form = normalizeForm(raw)
				freezeDesignConstraints(j.Form, raw)
				vid := "isolated-" + newID(10)
				mustExec(t, "UPDATE jobs SET form=$2,visitor=$3 WHERE id=$1", j.ID, mustJSON(j.Form), vid)
				commercialGenerator(t, nil)
				if err := stageDirections(j); err != nil {
					t.Fatal(err)
				}
				if err := stageLogo(j); err != nil {
					t.Fatal(err)
				}
				snap := buildLogoDirectionSnapshot(loadLogoDirections(j)[chosen])
				c := constraintsFor(j.Form)
				snap.Constraints = &c
				j.DirectionSnapshot = &snap
				mustExec(t, "UPDATE jobs SET direction_snapshot=$2,logo_confirmed=true,status='running',stage='boards' WHERE id=$1", j.ID, mustJSON(snap))
				j.Stage = "boards"
				j.Status = "running"
				grantTestItems(t, j.ID, "logo_h", "worldview", "app_overview", "brand_story")
				if err := stageBoardsV3(j); err != nil {
					t.Fatal(err)
				}
				for kind, want := range map[string]string{"logo_h": "logo", "worldview": "direction", "app_overview": "direction"} {
					refs := referenceInputs(j, kind)
					if len(refs) != 2 || refs[0].Role != want {
						t.Fatalf("%s reference order: %v", kind, refs)
					}
				}
				response := commerceRequest(t, "GET", "/0brand/api/jobs/"+j.ID+"/download", vid, "")
				if response.Code != 200 {
					t.Fatalf("download HTTP %d: %s", response.Code, response.Body)
				}
				archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
				if err != nil {
					t.Fatal(err)
				}
				if len(archive.File) != 4 {
					t.Fatalf("ZIP items=%d", len(archive.File))
				}
				for _, file := range archive.File {
					r, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(r)
					r.Close()
					if err != nil || len(data) == 0 {
						t.Fatal("empty/broken ZIP entry")
					}
					if strings.HasSuffix(file.Name, ".png") {
						if _, err := imagePNG(data); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}
