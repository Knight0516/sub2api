/**
 * 醍醐测试（pelican-showcase）只读作品展示 API
 * 通过本站后端代理访问，上游 API Key 保存在服务端；仅需登录态。
 */

import { apiClient } from './client'

/** 作品清单 data 顶层结构。 */
export interface ShowcaseManifest {
  schema_version: number
  enabled: boolean
  result_scope: string
  poll_interval_seconds: number
  max_items_per_group: number
  retention_days: number
  latest_generated_at: string
  groups: ShowcaseGroup[]
}

/** 作品分组。 */
export interface ShowcaseGroup {
  id: number
  name: string
  platform: string
  items: ShowcaseItemSummary[]
}

/** 作品摘要（清单内不含完整 HTML 正文）。 */
export interface ShowcaseItemSummary {
  id: number
  group_id: number
  model_id: string
  reasoning_effort: string
  status: string
  latency_ms: number
  generated_at: string
  content_url: string
}

/** 作品正文（response_text 为自包含 HTML，需在隔离 iframe 中渲染）。 */
export interface ShowcaseItem extends ShowcaseItemSummary {
  response_text: string
}

/** 读取作品清单。 */
export async function getShowcaseManifest(options?: { signal?: AbortSignal }): Promise<ShowcaseManifest> {
  const { data } = await apiClient.get<ShowcaseManifest>('/showcase', {
    signal: options?.signal,
  })
  return data
}

/** 读取单个作品正文。 */
export async function getShowcaseItem(id: number, options?: { signal?: AbortSignal }): Promise<ShowcaseItem> {
  const { data } = await apiClient.get<ShowcaseItem>(`/showcase/items/${id}`, {
    signal: options?.signal,
  })
  return data
}

export const showcaseAPI = {
  getShowcaseManifest,
  getShowcaseItem,
}

export default showcaseAPI
