package operations

import (
	"bytes"
	"encoding/json"
	"html/template"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func PrepareReviewBundleFile(root, input, output string, maxBytes int) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var request ReviewBundleRequest
		if d.DecodePrivate(raw, &request) != nil {
			return nil, d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		bundle, err := PrepareReviewBundle(request)
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(bundle, "", "  ")
	})
}

func RenderReviewBundleFile(root, input, output string, maxBytes int) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var bundle ReviewBundle
		if d.DecodePrivate(raw, &bundle) != nil {
			return nil, d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		if err := ValidateReviewBundle(bundle); err != nil {
			return nil, err
		}
		var result bytes.Buffer
		if err := reviewBundlePage.Execute(&result, bundle); err != nil {
			return nil, d.Fail("REVIEW_BUNDLE_INVALID", 422)
		}
		return result.Bytes(), nil
	})
}

// Never interpolate untrusted content as template.HTML: scripts, attributes and
// decoded entity text are all displayed as text, never as an executable page.
var reviewBundlePage = template.Must(template.New("private-evidence-review").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<title>Factorforge 私有原文审阅材料</title><style>
body{margin:0;background:#f3f7fa;color:#17384a;font:16px/1.7 system-ui,sans-serif}main{max-width:1120px;margin:auto;padding:24px}h1{font-size:26px}h2{scroll-margin-top:20px}nav a{display:inline-block;margin:0 16px 8px 0;color:#006e82}.notice,details{background:white;border:1px solid #b9ced8;padding:16px;border-radius:8px;margin:16px 0}.notice{border-left:5px solid #d09b39}.scroll{overflow:auto;background:white;margin:16px 0}table{border-collapse:collapse;width:100%;font-size:14px}th,td{border:1px solid #b9ced8;padding:10px;vertical-align:top}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:650px;overflow:auto}td:last-child{min-width:260px;white-space:pre-wrap;overflow-wrap:anywhere}code,p{overflow-wrap:anywhere}a{color:#006e82}small{color:#45677a}@media(max-width:600px){main{padding:14px}h1{font-size:22px}}
</style></head><body><main><h1>Factorforge 私有原文审阅材料</h1>
<nav><a href="#identity">身份与时间</a><a href="#paragraphs">原始段落</a><a href="#tokens">文字与字节坐标</a><a href="#original">完整原文</a></nav>
<div class="notice">状态：REVIEW_REQUIRED。此页没有审阅答案或交易资格。解码文字只供阅读，不能直接当原文字节跨度；全部原始段落仍须显式审阅。导航、脚本、隐藏元素和表格未自动删除，也未判定为事实。不要将真实原文或此页上传公开仓库。</div>
<h2 id="identity">身份与时间</h2><p>Bundle：<code>{{.BundleID}}</code><br>实例/环境：{{.Request.Binding.InstanceID}} / {{.Request.Binding.Environment}}<br>原文：{{.Proposal.Raw.EvidenceID}} / {{.Proposal.Raw.SourceID}}<br>原文 SHA-256：<code>{{.View.RawHash}}</code><br>媒体声明：{{.View.MediaType}}；原始字节：{{.View.RawBytes}}<br>首次公开：{{if .Proposal.Raw.FirstPublicAt}}{{.Proposal.Raw.FirstPublicAt}}{{else}}UNKNOWN{{end}}<br>供应商发布时间：{{if .Proposal.Raw.PublishedAt}}{{.Proposal.Raw.PublishedAt}}{{else}}UNKNOWN{{end}}<br>原始接收：{{.Proposal.Raw.ReceivedAt}}<br>许可引用：{{.Proposal.Raw.LicenceRef}}<br>来源网址（仅文字）：{{.Proposal.Raw.URL}}</p>
<p>警示：{{range .View.Warnings}}<code>{{.}}</code> {{end}}</p>
<h2 id="paragraphs">原始段落</h2><p>Proposal：<code>{{.Proposal.ProposalID}}</code>。坐标为 [start,end)，单位是 UTF-8 字节。候选目录命中及事件提示见配套 JSON；事件关系保持 UNKNOWN。</p>
<div class="scroll"><table><thead><tr><th>段落</th><th>字节范围</th><th>原词面（含标签）</th></tr></thead><tbody>{{range .Proposal.Paragraphs}}<tr><td>{{.ID}}</td><td>{{.Start}}–{{.End}}</td><td>{{.Text}}</td></tr>{{end}}</tbody></table></div>
<h2 id="tokens">文字与字节坐标</h2><p>TEXT 是 tokenizer 的解码文字，不是浏览器可见性或正文完整性判断。其他 token 的完整内容在原文中按坐标核对；实体、CRLF、注释及未闭合尾部不能被隐式忽略。</p>
<div class="scroll"><table><thead><tr><th>ID / 类型</th><th>原始字节</th><th>标签 / 阅读文字</th></tr></thead><tbody>{{range .View.Tokens}}<tr><td>{{.ID}}<br>{{.Kind}}</td><td>{{.Start}}–{{.End}}<br><small>{{.RawHash}}</small></td><td>{{.Tag}}{{.Text}}</td></tr>{{end}}</tbody></table></div>
<h2 id="original">完整原文</h2><details><summary>展开未改写原文（转义显示）</summary><pre>{{.Proposal.Raw.Content}}</pre></details>
<p>完成审阅后按对应设计生成 ReviewRequest，经 compile-evidence 编译并重新验证。此页不会访问模型、数据库或下层接口，也不会改写原时间。</p>
</main></body></html>`))
