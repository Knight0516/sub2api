<script setup lang="ts">
import { computed } from 'vue'
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()
const links = computed(() => (appStore.cachedPublicSettings?.header_links ?? [])
  .slice(0, 2)
  .map(item => ({ title: item.title.trim(), url: item.url.trim() }))
  .filter(item => {
    if (!item.title || !item.url) return false
    try {
      const url = new URL(item.url)
      return url.protocol === 'http:' || url.protocol === 'https:'
    } catch {
      return false
    }
  }))
</script>

<template>
  <a
    v-for="(item, index) in links"
    :key="index"
    :href="item.url"
    :title="item.title"
    target="_blank"
    rel="noopener noreferrer"
    data-testid="header-link"
    class="inline-flex min-w-0 shrink items-center rounded-lg px-2 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
  >
    <span class="max-w-20 truncate sm:max-w-40">{{ item.title }}</span>
  </a>
</template>
