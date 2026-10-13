<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-center">
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="relative w-full sm:w-80">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                placeholder="搜索作品 / 模型"
                class="input pl-10"
              />
            </div>
            <span v-if="manifest" class="text-sm text-gray-500 dark:text-gray-400">
              共 {{ filteredGroups.reduce((n, g) => n + g.items.length, 0) }} 个作品
            </span>
          </div>
          <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-3 lg:w-auto">
            <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadManifest">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <!-- 加载 / 错误状态 -->
        <div v-if="error" class="card flex flex-col items-center justify-center gap-2 py-16 text-center">
          <p class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
          <button type="button" class="btn btn-secondary btn-sm" @click="loadManifest">重试</button>
        </div>
        <div v-else-if="loading && !manifest" class="card flex items-center justify-center py-16">
          <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
        </div>
        <div v-else-if="filteredGroups.length === 0" class="card flex items-center justify-center py-16 text-sm text-gray-500 dark:text-gray-400">
          暂无作品
        </div>

        <!-- 作品网格 -->
        <div v-else class="flex flex-col gap-6 lg:min-h-0 lg:flex-1 lg:overflow-y-auto">
          <section v-for="group in filteredGroups" :key="group.id" class="card shrink-0 p-4">
            <header class="mb-3 flex items-center justify-between gap-3">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ group.name }}</h2>
              <span class="badge badge-gray">{{ group.items.length }}</span>
            </header>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
              <button
                v-for="item in group.items"
                :key="item.id"
                type="button"
                class="flex flex-col gap-2 rounded-lg border border-gray-200 bg-white p-3 text-left transition-colors hover:border-primary-300 hover:bg-primary-50 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-500/40 dark:hover:bg-dark-700"
                :class="{ 'ring-2 ring-primary-500': selectedItem?.id === item.id }"
                @click="openItem(item)"
              >
                <div class="flex items-center justify-between gap-2">
                  <span class="truncate font-medium text-gray-900 dark:text-white">#{{ item.id }}</span>
                  <span
                    v-if="item.status === 'success'"
                    class="inline-flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400"
                  >
                    {{ formatLatency(item.latency_ms) }}
                  </span>
                </div>
                <div class="truncate text-xs text-gray-500 dark:text-gray-400">
                  {{ item.model_id }}
                  <span v-if="item.reasoning_effort" class="ml-1 opacity-70">· {{ item.reasoning_effort }}</span>
                </div>
                <div class="text-xs text-gray-400 dark:text-gray-500">{{ formatTime(item.generated_at) }}</div>
              </button>
            </div>
          </section>
        </div>

        <!-- 作品正文（隔离 iframe 渲染） -->
        <teleport to="body">
          <transition name="fade">
            <div
              v-if="detailOpen"
              class="fixed inset-0 z-50 flex flex-col bg-black/60 p-4 sm:p-8"
              @click.self="closeDetail"
            >
              <div class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl bg-white shadow-2xl dark:bg-dark-800">
                <header class="flex items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
                  <div class="min-w-0">
                    <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                      作品 #{{ selectedItem?.id }}
                      <span v-if="selectedItem" class="ml-2 font-normal text-gray-500 dark:text-gray-400">{{ selectedItem.model_id }}</span>
                    </h3>
                    <p v-if="selectedItem" class="text-xs text-gray-400 dark:text-gray-500">
                      {{ formatTime(selectedItem.generated_at) }}
                    </p>
                  </div>
                  <button type="button" class="btn btn-secondary btn-sm flex-shrink-0" @click="closeDetail">
                    <Icon name="x" size="sm" />
                    <span class="ml-1">关闭</span>
                  </button>
                </header>
                <div class="relative min-h-0 flex-1 bg-gray-100 dark:bg-dark-900">
                  <div v-if="loadingItem" class="absolute inset-0 flex items-center justify-center">
                    <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
                  </div>
                  <iframe
                    v-if="itemHtml"
                    :srcdoc="itemHtml"
                    sandbox="allow-scripts allow-popups"
                    referrerpolicy="no-referrer"
                    class="h-full w-full border-0"
                    title="作品正文"
                  ></iframe>
                </div>
              </div>
            </div>
          </transition>
        </teleport>
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import showcaseAPI, {
  type ShowcaseGroup,
  type ShowcaseItem,
  type ShowcaseItemSummary,
  type ShowcaseManifest,
} from '@/api/showcase'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const appStore = useAppStore()

const manifest = ref<ShowcaseManifest | null>(null)
const loading = ref(false)
const error = ref('')

const searchQuery = ref('')
const loadingItem = ref(false)
const detailOpen = ref(false)
const selectedItem = ref<ShowcaseItem | null>(null)
const itemHtml = ref('')

let manifestController: AbortController | null = null
let itemController: AbortController | null = null

/** 按搜索词过滤分组与作品。 */
const filteredGroups = computed<ShowcaseGroup[]>(() => {
  if (!manifest.value) return []
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return manifest.value.groups
  return manifest.value.groups
    .map((g) => {
      if (g.name.toLowerCase().includes(q)) return g
      const items = g.items.filter((it) => String(it.id).includes(q) || it.model_id.toLowerCase().includes(q))
      if (items.length === 0) return null
      return { ...g, items }
    })
    .filter((g): g is ShowcaseGroup => g !== null)
})

async function loadManifest() {
  manifestController?.abort()
  const controller = new AbortController()
  manifestController = controller
  loading.value = true
  error.value = ''
  try {
    const result = await showcaseAPI.getShowcaseManifest({ signal: controller.signal })
    if (manifestController === controller) manifest.value = result
  } catch (err: unknown) {
    if (manifestController !== controller || isAbortError(err)) return
    error.value = extractApiErrorMessage(err, '作品清单加载失败')
  } finally {
    if (manifestController === controller) {
      manifestController = null
      loading.value = false
    }
  }
}

async function openItem(summary: ShowcaseItemSummary) {
  itemController?.abort()
  const controller = new AbortController()
  itemController = controller
  detailOpen.value = true
  loadingItem.value = true
  itemHtml.value = ''
  // 先用摘要填充头部信息，正文加载完成后替换。
  selectedItem.value = { ...summary, response_text: '' }
  try {
    const item = await showcaseAPI.getShowcaseItem(summary.id, { signal: controller.signal })
    if (itemController !== controller) return
    selectedItem.value = item
    itemHtml.value = item.response_text || ''
  } catch (err: unknown) {
    if (itemController !== controller || isAbortError(err)) return
    appStore.showError(extractApiErrorMessage(err, '作品正文加载失败'))
    detailOpen.value = false
  } finally {
    if (itemController === controller) {
      itemController = null
      loadingItem.value = false
    }
  }
}

function closeDetail() {
  itemController?.abort()
  itemController = null
  detailOpen.value = false
  loadingItem.value = false
  selectedItem.value = null
  itemHtml.value = ''
}

function formatLatency(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`
  return `${ms}ms`
}

function formatTime(value: string): string {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

function isAbortError(err: unknown): boolean {
  return (
    (typeof err === 'object' && err !== null && (err as { code?: string }).code === 'ERR_CANCELED') ||
    (err instanceof DOMException && err.name === 'AbortError')
  )
}

// 清单接口建议 >=60s 轮询；这里用定时静默刷新，避免高频打上游。
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  void loadManifest()
  pollTimer = setInterval(() => {
    if (!loading.value && !detailOpen.value) void loadManifest()
  }, 60_000)
})

onBeforeUnmount(() => {
  manifestController?.abort()
  itemController?.abort()
  if (pollTimer) clearInterval(pollTimer)
})
</script>
