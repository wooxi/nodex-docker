<template>
  <div>
    <div class="card">
      <div class="card-title"><el-icon><Document /></el-icon>运行日志</div>
      <div class="log-toolbar">
        <el-select v-model="nodeId" size="small" style="width:180px" placeholder="选择节点" @change="load">
          <el-option v-for="n in nodes" :key="n.id" :label="n.name" :value="n.id" />
        </el-select>
        <el-radio-group v-model="type" size="small" @change="load">
          <el-radio-button value="error">错误日志</el-radio-button>
          <el-radio-button value="access">访问日志</el-radio-button>
        </el-radio-group>
        <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
        <el-switch v-model="auto" active-text="自动刷新" size="small" style="margin-left:12px" />
      </div>
      <pre class="log-view" v-loading="loading">{{ logs || '（暂无日志）' }}</pre>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'

const nodes = ref([])
const nodeId = ref('')
const logs = ref('')
const type = ref('error')
const loading = ref(false)
const auto = ref(true)
let timer = null

async function load() {
  if (!nodeId.value) return
  loading.value = true
  try {
    const res = await api.get(`/api/logs?node=${nodeId.value}&type=${type.value}`)
    logs.value = res.logs
  } catch (e) {} finally { loading.value = false }
}

onMounted(async () => {
  try {
    const res = await api.get('/api/status')
    nodes.value = res.nodes || []
    if (nodes.value.length) {
      nodeId.value = nodes.value[0].id
      load()
    }
  } catch (e) {}
  timer = setInterval(() => { if (auto.value) load() }, 5000)
})
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.log-toolbar { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.log-view { background: #0d1117; color: #c9d1d9; border-radius: 6px; padding: 14px; font-size: 12px;
  line-height: 1.6; height: 60vh; overflow-y: auto; white-space: pre-wrap; word-break: break-all; font-family: 'JetBrains Mono', Consolas, monospace; }
</style>
