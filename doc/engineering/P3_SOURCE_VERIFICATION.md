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
