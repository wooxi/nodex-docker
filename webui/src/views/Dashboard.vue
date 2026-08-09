<template>
  <div>
    <!-- 总览卡片 -->
    <div class="stat-row">
      <div class="stat-card">
        <div class="stat-label">节点总数</div>
        <div class="stat-value">{{ status.total || 0 }}</div>
        <div class="stat-sub">运行中 {{ status.running || 0 }} 个</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">在线用户</div>
        <div class="stat-value">{{ onlineCount }}</div>
        <div class="stat-sub">近 10 分钟活跃</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">总流量</div>
        <div class="stat-value">{{ fmtBytes(totalTraffic) }}</div>
        <div class="stat-sub">所有节点累计</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">面板对接</div>
        <div class="stat-value">{{ panelOkCount }}/{{ status.total || 0 }}</div>
        <div class="stat-sub">同步正常节点数</div>
      </div>
    </div>

    <!-- 内核总开关 -->
    <div class="card">
      <div class="card-title"><el-icon><Switch /></el-icon>内核总开关</div>
      <el-table :data="kernelRows" size="small">
        <el-table-column prop="name" label="内核" width="120" />
        <el-table-column label="状态" width="160">
          <template #default="{ row }">
            <el-tag :type="row.state === '运行中' ? 'success' : (row.state === '已停止' ? 'danger' : 'warning')" size="small">{{ row.state }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作">
          <template #default="{ row }">
            <el-button size="small" type="danger" :loading="kernelActing === row.action" @click="kernelAction('stop-' + row.action, row.name)">全部停止</el-button>
            <el-button size="small" type="success" :loading="kernelActing === 'start-' + row.action" @click="kernelAction('start-' + row.action, row.name)">全部启动</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 节点状态 -->
    <div class="card">
      <div class="card-title"><el-icon><Connection /></el-icon>节点状态</div>
      <div v-for="n in status.nodes || []" :key="n.id" class="node-row">
        <div class="node-info">
          <el-tag type="primary" size="small" effect="plain">{{ protoLabel(n.protocol) }}</el-tag>
          <el-tag :type="n.enabled ? (n.xray.running ? 'success' : 'warning') : 'info'" size="small">
            {{ n.enabled ? (n.xray.running ? '运行中' : '已停止') : '已禁用' }}
          </el-tag>
          <b>{{ n.name }}</b>
        </div>
        <div class="node-vers">
          <span class="v-item">xray: <b>{{ n.xray.running ? '●' : '○' }}</b> {{ shortVer(n.xray.version) }}</span>
          <span class="v-item">hysteria2: <b>{{ n.hy2.running ? '●' : '○' }}</b> {{ shortVer(n.hy2.version) }}</span>
        </div>
        <div class="node-panel" :class="n.panel.lastError ? 'err' : ''">
          {{ n.panel.lastError ? '⚠ ' + n.panel.lastError : '面板同步正常' }}
        </div>
        <div class="node-ops">
          <el-button size="small" type="success" @click="act('restart', n.id)">重启</el-button>
          <el-button size="small" @click="$router.push('/nodes/' + n.id)">配置</el-button>
        </div>
      </div>
      <el-empty v-if="!(status.nodes || []).length" description="暂无节点，请到「节点管理」新增" />
    </div>

    <!-- 用户流量 -->
    <div class="card">
      <div class="card-title"><el-icon><User /></el-icon>用户流量（累计）</div>
      <el-table :data="users" size="small" v-loading="loadingUsers" empty-text="暂无流量数据">
        <el-table-column prop="node_name" label="节点" width="120" />
        <el-table-column prop="uid" label="用户 ID" width="100" />
        <el-table-column label="累计流量" width="150">
          <template #default="{ row }"><b>{{ fmtBytes(row.traffic) }}</b></template>
        </el-table-column>
        <el-table-column label="在线 IP">
          <template #default="{ row }">
            <el-tag v-for="ip in (row.ips || [])" :key="ip" size="small" style="margin-right:4px">{{ ip }}</el-tag>
            <span v-if="!row.ips || !row.ips.length" class="offline">离线</span>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Connection, User } from '@element-plus/icons-vue'
import { api, fmtBytes } from '../api'

const status = ref({})
const users = ref([])
const loadingUsers = ref(false)
let timer = null

const onlineCount = computed(() => users.value.filter(u => u.ips && u.ips.length).length)
const totalTraffic = computed(() => users.value.reduce((s, u) => s + (u.traffic || 0), 0))
const panelOkCount = computed(() => (status.value.nodes || []).filter(n => n.panel.running && !n.panel.lastError).length)
const kernelActing = ref('')

const kernelRows = computed(() => {
  const nodes = status.value.nodes || []
  const enabled = nodes.filter(n => n.enabled)
  const total = enabled.length
  const summarize = (key) => {
    const on = enabled.filter(n => n[key] && n[key].running).length
    if (on === 0) return '已停止'
    if (on === total) return '运行中'
    return on + '/' + total + ' 运行'
  }
  return [
    { name: 'Xray', action: 'xray', state: summarize('xray') },
    { name: 'Hysteria2', action: 'hy2', state: summarize('hy2') }
  ]
})

async function kernelAction(action, name) {
  kernelActing.value = action
  try {
    await api.post('/api/action', { action })
    ElMessage.success(name + ' 操作成功')
    setTimeout(refresh, 1500)
  } catch (e) { ElMessage.error(e.message) } finally { kernelActing.value = '' }
}

function shortVer(v) {
  if (!v || v === '未知') return v || '未知'
  return v.split(' ')[0] || v
}

const PROTO_LABELS = { vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'SS', hysteria2: 'Hysteria2' }
function protoLabel(p) { return PROTO_LABELS[p] || p || '未知' }

async function refresh() {
  try {
    status.value = await api.get('/api/status')
    const res = await api.get('/api/users')
    users.value = res.users
  } catch (e) {}
}

async function act(action, id) {
  try {
    await api.post('/api/action', { action, node_id: id })
    ElMessage.success('操作成功')
    setTimeout(refresh, 2000)
  } catch (e) { ElMessage.error(e.message) }
}

onMounted(() => { refresh(); timer = setInterval(refresh, 10000) })
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.stat-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 16px; }
.stat-card { background: #fff; border-radius: 8px; padding: 20px; box-shadow: 0 1px 4px rgba(0,0,0,.06); }
.stat-label { color: #909399; font-size: 13px; margin-bottom: 10px; }
.stat-value { font-size: 24px; font-weight: 600; }
.stat-sub { color: #c0c4cc; font-size: 12px; margin-top: 8px; }
.node-row { display: flex; align-items: center; gap: 16px; padding: 12px 0; border-bottom: 1px solid #f0f2f5; flex-wrap: wrap; }
.node-row:last-child { border-bottom: none; }
.node-info { display: flex; align-items: center; gap: 8px; min-width: 180px; }
.node-meta { color: #c0c4cc; font-size: 12px; }
.node-vers { display: flex; gap: 16px; font-size: 13px; color: #606266; }
.v-item b { color: #67c23a; }
.node-panel { font-size: 12px; color: #909399; flex: 1; min-width: 150px; }
.node-panel.err { color: #f56c6c; }
.offline { color: #c0c4cc; font-size: 12px; }
@media (max-width: 900px) { .stat-row { grid-template-columns: repeat(2, 1fr); } }
</style>
