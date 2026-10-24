<script setup lang="ts">
import { computed } from 'vue'
import { brand } from '../brand'

/**
 * 品牌标记。内置时渲染矢量标记（可跟随主题换底色），
 * 自定义时渲染用户上传的图片。
 *
 * 抽成组件是为了让侧栏、设置页预览、其它入口共用同一套判断——
 * 否则「哪里显示 SVG、哪里显示 img」会散落在各处，改一处漏一处。
 */
const props = withDefaults(defineProps<{ size?: number }>(), { size: 32 })

const custom = computed(() => brand.value.logo_source === 'upload' && !!brand.value.logo_url)

// 上传的图多半是方形且不带圆角，这里补一个圆角，
// 否则贴在圆角侧栏里会显得比周围的元素「方」。
const imgRadius = computed(() => `${Math.round(props.size * 0.22)}px`)
</script>

<template>
  <img
    v-if="custom"
    :src="brand.logo_url"
    :width="size"
    :height="size"
    :style="{ borderRadius: imgRadius }"
    class="brand-mark-img"
    alt=""
  />
  <svg v-else :width="size" :height="size" viewBox="0 0 32 32" class="brand-mark-svg" aria-hidden="true">
    <path class="mark-bg" fill="#171717" d="M1 9a8 8 0 0 1 8-8h12l10 10v12a8 8 0 0 1-8 8H9a8 8 0 0 1-8-8V9Z" />
    <path fill="#ffffff" fill-opacity="0.2" d="M21 1 31 11H21V1Z" />
    <g fill="none" stroke-linecap="round">
      <path stroke="#ffffff" stroke-opacity="0.46" stroke-width="2.4" d="M8 12.4h15" />
      <path stroke="var(--accent)" stroke-width="3" d="M8 18.2h16" />
      <path stroke="#ffffff" stroke-opacity="0.46" stroke-width="2.4" d="M8 24h6.5" />
    </g>
  </svg>
</template>

<style scoped>
.brand-mark-img,
.brand-mark-svg {
  display: block;
  object-fit: cover;
}
</style>
