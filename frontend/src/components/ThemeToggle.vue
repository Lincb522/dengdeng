<script setup lang="ts">
import { computed, useAttrs } from 'vue'
import { themePresets, useTheme, type ColorMode } from '../stores/theme'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const theme = useTheme()
const attrs = useAttrs()
const { trigger, panel, open, panelStyle, clearCloseTimer, scheduleClose, togglePinned, close } = useAnchoredPopover('end')
const panelID = `theme-switcher-${Math.random().toString(36).slice(2)}`

const modeOptions: { id: ColorMode; label: string; icon: string }[] = [
  { id: 'light', label: '浅色', icon: 'M12 4.75a7.25 7.25 0 1 0 7.25 7.25A7.26 7.26 0 0 0 12 4.75Zm0-2.5v1.2m0 17.1v1.2M4.75 4.75l.85.85m12.8 12.8.85.85M2.25 12h1.2m17.1 0h1.2M4.75 19.25l.85-.85m12.8-12.8.85-.85' },
  { id: 'dark', label: '深色', icon: 'M20.4 14.6A8.2 8.2 0 0 1 9.4 3.6a8.2 8.2 0 1 0 11 11Z' },
  { id: 'system', label: '自动', icon: 'M3 4h18v12H3V4zm7 14h4m-6 2h8' },
]

const label = computed(() => `界面主题：${theme.activeTheme.name}，${theme.colorMode === 'system' ? '跟随系统' : theme.isDark ? '深色' : '浅色'}`)

function selectTheme(themeID: (typeof themePresets)[number]['id']) {
  theme.setTheme(themeID)
  close()
}

function selectMode(mode: ColorMode) {
  theme.setColorMode(mode)
  close()
}
</script>

<template>
  <button
	v-bind="attrs"
    ref="trigger"
    type="button"
    class="theme-toggle"
    :class="{ 'is-open': open }"
    :aria-label="label"
    :title="label"
    :aria-expanded="open"
    :aria-controls="panelID"
    @click.stop="togglePinned"
  >
    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3a9 9 0 1 0 0 18h1.25a1.75 1.75 0 0 0 0-3.5H12a1.5 1.5 0 0 1 0-3h2.8A6.2 6.2 0 0 0 21 8.3C21 5.37 17.1 3 12 3ZM7.3 9.2h.01M9.2 6.5h.01M13 5.8h.01M16.6 7.3h.01" /></svg>
    <span class="theme-toggle-label">{{ theme.activeTheme.name }}</span>
    <svg class="theme-toggle-chevron" viewBox="0 0 20 20" aria-hidden="true"><path d="m6 8 4 4 4-4" /></svg>
  </button>

  <Teleport to="body">
    <Transition name="theme-panel-pop">
      <section
        v-if="open"
        :id="panelID"
        ref="panel"
        class="theme-panel"
        :style="panelStyle"
        aria-label="界面主题"
        @mouseenter="clearCloseTimer"
        @mouseleave="scheduleClose"
      >
        <header class="theme-panel__head">
          <strong>界面主题</strong>
          <span>布局、密度与组件会一起改变</span>
        </header>

        <div class="theme-preset-list">
          <button
            v-for="preset in themePresets"
            :key="preset.id"
            type="button"
            class="theme-preset"
            :class="[`is-${preset.layout}`, { 'is-active': theme.themeID === preset.id }]"
            :aria-pressed="theme.themeID === preset.id"
            @click="selectTheme(preset.id)"
          >
            <span class="theme-preset__preview" :style="{ '--preview-canvas': preset.colors[0], '--preview-ink': preset.colors[1], '--preview-accent': preset.colors[2] }">
              <i class="theme-preset__nav"></i>
              <i class="theme-preset__bar"></i>
              <i class="theme-preset__block theme-preset__block--one"></i>
              <i class="theme-preset__block theme-preset__block--two"></i>
            </span>
            <span class="theme-preset__copy">
              <strong>{{ preset.name }}</strong>
              <small>{{ preset.description }}</small>
            </span>
            <svg v-if="theme.themeID === preset.id" viewBox="0 0 20 20" aria-hidden="true"><path d="m5 10 3.2 3.2L15 6.5" /></svg>
          </button>
        </div>

        <div class="theme-mode-picker" aria-label="显示模式">
          <button
            v-for="option in modeOptions"
            :key="option.id"
            type="button"
            :class="{ 'is-active': theme.colorMode === option.id }"
            :aria-pressed="theme.colorMode === option.id"
            @click="selectMode(option.id)"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="option.icon" /></svg>
            <span>{{ option.label }}</span>
          </button>
        </div>
      </section>
    </Transition>
  </Teleport>
</template>
