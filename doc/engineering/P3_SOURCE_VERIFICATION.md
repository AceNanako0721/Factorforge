# P3 真实来源验证

日期：2026-10-09。这是来源方法证据，不是已生效生产登记。

## 美联储理事会第一方正文

官方 [RSS 目录](https://www.federalreserve.gov/feeds/feeds.htm) 明确发布货币政策 RSS；[网站声明](https://www.federalreserve.gov/disclaimer.htm) 的 Copyright/trademark 条款说明未另行标注的理事会信息属于公共领域，复制分发应署名；第三方内容、图片/标识和外链不能自动继承许可。本次只将其第一方文字公告作为候选，不授权复制站点标识或第三方材料，不把整站页面包装成我们的页面。尚未安装来源、有效期、对象映射或 Provider 外发政策。

开发机通过当前生产 Fetcher 的 HTTPS/公开 DNS/无跳转/有界响应路径读 https://www.federalreserve.gov/feeds/press_monetary.xml，text/xml，9649 字节、15 条；SHA256 bf780e2be29eefecb959a41f8b4ce7a333fb65e618470d983ecd199dfab128e7。仅选择第一项原文做方法探针，并没有将生产 RSSSource 改成截断前一项；其 MaxItems 仍是完整 feed 条数预算，配置小于15会明确拒绝，不是每轮原文调用次数预算。

原文 https://www.federalreserve.gov/newsevents/pressreleases/monetary20261007a.htm，text/html，82414 字节；SHA256 80bacf8f8702d6e3dbef306fe82af5e0fe6c316e0aac3b3445c6aadf43aa3bfb。供应商 pubDate 是 Wed, 7 Oct 2026 18:00:00 GMT，实际接收 2026-10-09T05:18:03.657024321Z；provider_published_at 转为 2026-10-07T18:00:00Z，first_public_at 保留 UNKNOWN，不把抓取时间、HTTP时间或 feed 日期变成独立核验的首发时刻。

## 复现及修复

首次试验在取到 feed 后因 SOURCE_LAB_PUBLICATION_UNKNOWN 停止，未抓原文。既有 time.RFC1123/time.RFC1123Z 要求两位日期，不能读取该官方一位日期；生产 Poll 会跳过这项。修复为 ParseRSSPublished 支持明确的一位/两位日期、数字时区或字面 UTC/GMT。未知命名时区不能由 time.Parse 虚构为零偏移；未识别时间与未来时间沿用原控制流。本项属于已发布 RSS 设计的格式兼容修复，不改变式样、资源边界或首发政策。

修复后新试验成功取到一份原文；两轮共三个开发机 GET，无自动重试、JEV、存储/框架写入或交易。first-failed-trial-feed.xml、feed.xml、original.html、report.json 仅在忽略的 runtime/source-lab-20261009，原文件0600/目录0700。可复跑代码为 tests/applications/soxl_jev/experiments/source_lab_test.go；普通 CI 跳过网络探针，只显式实验目录可启用。

本地真实 HTTP 夹具验证同样日期不再漏项、数字 -0400 转换、GMT/UTC、一位/两位、未知时区/非法日期拒绝、未来截止拒绝、未授权首发时间保持 null。单次源探针不证明全天轮询、召回完整性、生产语义抽取、JEV 外发许可、共享负载或收益；旧公告不能作为实时事件产生增险交易。

## Federal Register / GovInfo：元数据、原文与时间边界

2026-10-09T07:21:59Z～07:28:01Z，开发机六次匿名 GET，无重试、JEV、数据库/下层写入或订单。先读 JSON/RSS 列表，再读列表中 2026-20716 的详情，然后按详情返回的地址读 HTML/XML 正文和 GovInfo 官方 PDF。只留私有归档，没有安装来源、许可、映射或生产 Prompt。

官方 [API 文档](https://www.federalregister.gov/developers/documentation/api/v1) 明确无需 API Key，同时区分本站信息性 XML 与 GovInfo 官方法律版本。官方 [GovInfo 政策](https://www.govinfo.gov/about/policies) 说明政府作品的一般公共领域规则及第三方受版权内容/图片例外。因此本轮只能证明取得元数据与候选正文，不能给整站或模型外发作一揽子许可，也没有把非官方 XML 称作正式法律版本。

固定查询条件为 term=semiconductor、publication_date[lte]=2026-10-09、order=newest、per_page=3，分别请求 /api/v1/documents.json 和 .rss。JSON 返回三条（总计 3810）；RSS 返回 17 条，前三条 URL 与 JSON 身份一致，后续包括其他相关或宽泛匹配公告。这只是这一组查询的观察，不能断言该参数对所有 RSS 都被忽略，也不能把总数/关键词命中当作财经召回或对象映射。

| 私有归档 | 字节 | SHA256 |
| --- | ---: | --- |
| documents.json | 6197 | 8b7051d3dd957603374d84457441290e2c30a65b179ef157322fd550785919ca |
| documents.rss | 20664 | cb334c12ec85e2c7bf6ae04c2667486252028b04deb2c6ae63da38e11655eff5 |
| document.json | 2762 | eb84917927ba505ac1b327c1d831a7d54b844c9a0e5017371a94dda12540e150 |
| original.html | 9982 | 541345a73cd877ff7cf2e607f4a00009b4bb82af7bef7828659ff1a4545964a1 |
| original.xml | 7483 | 86faa15f8a11ce0dfea9e070290e4b94c0414ddac727fa508bb0b28edb3e92a6 |
| official.pdf | 206722 | c78add9c0f691c24b34f347d02635c36db62a334a43be6fb8293ef223e76f4b3 |

详情为 International Trade Commission 的 DRAM 调查终止通知，91 FR 64678；正文地址由 body_html_url / full_text_xml_url 提供，官方 PDF 为 https://www.govinfo.gov/content/pkg/FR-2026-10-09/pdf/2026-20716.pdf。详情 publication_date=2026-10-09、public_inspection_pdf_url=null，RSS pubDate=2026-10-09T04:00:00Z；正文的签发日期为 10 月 6 日，并标注 Filed 10-8-26; 8:45 am。官方 PDF 对目标文号的 Filed 标记一致，但同页还包含 2026-20715 及其他公告，不能把整页文字当作目标事件原文。PDF 由已有 pdftotext -layout -enc UTF-8 离线查看，破折号仅为对照归一化，没有改原始字节或生产抽取器。

这些时间各有不同语义；Filed 标记本身没有明确时区，也未独立证明最早可用时刻。缺 public-inspection 链接不证明此前未公开，午夜形式的 RSS 日期、正文签发日期、PDF 排版时间及抓取时间均不能代替 first_public_at，继续 UNKNOWN。某一个通知可读也不能证明来源组合、持续新鲜度或 SOXL 相关性完整。

register_replay_test.go 按归档哈希做无公网重放：现有 RSSSource 配 MaxItems=3 对 17 条完整 feed 在读原文前明确拒绝；仅为实验投影的一条 feed 可保留 HTML 全字节和 provider_published_at，并保持 first_public_at=null。XML 正文仍被当前生产格式边界以 MISSING_ORIGINAL 拒绝。实验的一条投影不是生产截断或新适配器；现有 RSS 调用也不会自动读取详情/改抓 body_html_url。未来若采用结构化 API/详情时间或 XML，须先完成相应试验和设计，不在本次代码里接入。

可复跑代码为 reference_lab_test.go / register_replay_test.go，全部 opt-in；采集已有文件或 started 标记时在网络前拒绝，403/429 保存事实后停止该组请求，不用格式变体继续探测。private runtime/register-{source,detail,original,pdf,replay}-lab-20261009 目录 0700、文件 0600；原文及 PDF 不进入公开仓库。SEC 既有 403 没有重试或绕过。
