package app

// Durable generation metadata. This is separate from delivery rights and from
// the existing job_images rows. It intentionally stores no API keys or headers.
import (
	"context"
	"encoding/json"
	"fmt"
)

func refsFromPaths(paths []string) []referenceImage {
	out := make([]referenceImage, 0, len(paths))
	for i, p := range paths {
		out = append(out, referenceImage{Path: p, Role: fmt.Sprintf("input_%d", i+1), Label: fmt.Sprintf("输入参考图 %d", i+1)})
	}
	return out
}

func recordGeneration(jobID, step, kind string, idx int, status, prompt, model, requestID, parent string, refs []referenceImage, responseImage, revisedPrompt, errText string, tokens int) {
	refsJSON, _ := json.Marshal(refs)
	params, _ := json.Marshal(map[string]any{"size": "1024x1280", "n": 1})
	_, _ = pool.Exec(context.Background(), `INSERT INTO generation_records
		(job_id,step,kind,idx,status,prompt_template_version,prompt,provider,model,request_id,parent_image,refs,params,response_image,revised_prompt,error,tokens)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		jobID, step, kind, idx, status, directionConfigVersion+"/"+designConstraintsVersion, prompt, "openai-compatible", model,
		requestID, parent, string(refsJSON), string(params), responseImage, revisedPrompt, errText, tokens)
}
