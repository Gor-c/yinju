// 入库失败原因的「翻译层」。
//
// 后端把底层错误原样存进 index_jobs.message（Chroma 的 404 JSON、pgx 的
// 连接错误、嵌入服务的维度报错…），对运维有用，对使用者是一堵墙。
// 这里只做一件事：把已知的几类错误翻成人话 + 给一句下一步该做什么，
// 原文仍然保留，放在提示里，不丢信息。

export interface IndexErrorInfo {
  /** 一句话结论，直接显示在列表里 */
  title: string
  /** 下一步做什么 */
  hint: string
  /** 原始错误，放在 tooltip 里备查 */
  raw: string
}

export function describeIndexError(raw?: string | null): IndexErrorInfo {
  const text = String(raw || '').trim()
  if (!text) {
    return { title: '解析失败，未记录原因', hint: '重试一次；仍失败可查看服务日志。', raw: '' }
  }

  // 向量库集合被重建/清理后，运行中的服务仍拿着旧集合 ID。
  if (/Collection .* does not exist|NotFoundError|upsert returned 404/i.test(text)) {
    return {
      title: '向量库集合已失效',
      hint: '向量库被重建或清理过，服务缓存的集合 ID 过期了。点「重新解析」即可，服务会重新解析集合并写入。',
      raw: text,
    }
  }

  // 嵌入模型输出维度与配置不一致：这类错误不修配置，重试多少次都一样。
  const dim = text.match(/embedding dimension is (\d+), expected (\d+)/i)
  if (dim) {
    return {
      title: `向量维度不一致（模型输出 ${dim[1]} 维，配置要求 ${dim[2]} 维）`,
      hint: '把 EMBED_DIMENSIONS 改成模型实际输出维度，或在设置页换回原模型，然后重建索引。重试不会自愈。',
      raw: text,
    }
  }

  if (/context deadline exceeded|timed out|timeout|超时/i.test(text)) {
    return {
      title: '处理超时',
      hint: '文件较大或嵌入服务响应慢。可拆成多个文件重传，或调大 INDEX_TIMEOUT_MIN 后重试。',
      raw: text,
    }
  }

  if (/no such file|file not found|open .*: no such|cannot find the file/i.test(text)) {
    return {
      title: '原始文件已丢失',
      hint: '上传目录里的源文件不在了（可能被手工清理）。重新上传这份文档即可。',
      raw: text,
    }
  }

  if (/connection refused|dial tcp|no such host|i\/o timeout|EOF/i.test(text)) {
    return {
      title: '依赖服务连不上',
      hint: '向量库或嵌入服务没启动，或地址端口配错了。确认服务在线后重试。',
      raw: text,
    }
  }

  if (/未提取到文本|extracted no text|empty document/i.test(text)) {
    return {
      title: '没有提取到文本',
      hint: '多为扫描件或纯图片 PDF，需要先做 OCR。也可以直接上传可复制的版本。',
      raw: text,
    }
  }

  const apiStatus = text.match(/returned (\d{3})/)
  if (apiStatus) {
    return {
      title: `上游接口返回 ${apiStatus[1]}`,
      hint: '多半是模型或向量服务的地址、密钥、额度问题。展开原文确认是哪一步。',
      raw: text,
    }
  }

  // 未知错误：截断展示，原文仍可展开。
  const brief = text.length > 48 ? text.slice(0, 48) + '…' : text
  return { title: brief, hint: '展开查看完整原因；如需上报，请附上原文。', raw: text }
}
