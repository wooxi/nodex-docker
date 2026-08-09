<template>
  <div>
    <!-- 系统状态仪表盘（大框整合） -->
    <div class="dash-card">
      <div class="dash-body">
        <div class="dash-item">
          <div class="dash-num">{{ status.total || 0 }}</div>
          <div class="dash-lbl">节点</div>
        </div>
        <div class="dash-sep"></div>
        <div class="dash-item">
          <div class="dash-num" :class="{ ok: running > 0 }">{{ running }}</div>
          <div class="dash-lbl">运行中</div>
        </div>
        <div class="dash-sep"></div>
        <div class="dash-item">
          <div class="dash-num" :class="{ ok: onlineCount > 0 }">{{ onlineCount }}</div>
          <div class="dash-lbl">在线用户</div>
        </div>
        <div class="dash-sep"></div>
        <div class="dash-item">
          <div class="dash-num">{{ fmtBytes(totalTraffic) }}</div>
          <div class="dash-lbl">总流量</div>
        </div>
      </div>
    </div>

    <!-- 内核总开关 + 节点列表（电脑端并排） -->
    <div class="dual-col">
      <div class="card">
        <div class="card-title"><el-icon><Switch /></el-icon>内核总开关</div>
        <el-table :data="kernelRows" size="small">
          <el-table-column prop="name" label="内核" width="110" />
          <el-table-column label="节点状态" width="200">
            <template #default="{ row }">
              <span v-for="(dot, i) in row.dots" :key="i" class="kernel-dot" :class="dot ? 'on' : 'off'"></span>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="120">
            <template #default="{ row }">
              <el-tag :type="row.stateType" size="small">{{ row.state }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" align="right">
            <template #default="{ row }">
              <el-button size="small" type="danger" :loading="kernelActing === 'stop-' + row.action" @click="kernelAction('stop-' + row.action, row.name)">停止</el-button>
              <el-button size="small" type="success" :loading="kernelActing === 'start-' + row.action" @click="kernelAction('start-' + row.action, row.name)">启动</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <div class="card">
        <div class="card-title"><el-icon><Connection /></el-icon>节点列表</div>
        <el-table :data="nodes" size="small" v-loading="loading">
          <el-table-column label="节点" min-width="140">
            <template #default="{ row }"><b>{{ row.name }}</b></template>
          </el-table-column>
          <el-table-column label="协议类型" width="120">
            <template #default="{ row }">
              <el-tag type="primary" size="small" effect="plain">{{ protoLabel(row.protocol) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="nodeStateType(row)" size="small">{{ nodeStateText(row) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="160">
            <template #default="{ row }">
              <el-button size="small" @click="act('restart', row.id)">重启</el-button>
              <el-button size="small" @click="$router.push('/nodes/' + row.id)">配置</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!nodes.length" description="暂无节点，请到「节点管理」新增" :image-size="60" />
      </div>

    <!-- 用户流量（第三列） -->
    <div class="card">
      <div class="card-title">
        <el-icon><User /></el-icon>用户流量
        <span class="sync-badge" :class="syncBadgeClass" :title="syncBadgeTitle">
          ● 面板同步{{ syncBadgeText }}
        </span>
      </div>
      <el-table :data="users" size="small" v-loading="loadingUsers" empty-text="暂无流量数据（用户连接节点后自动统计）">
        <el-table-column prop="node_name" label="节点" width="110" />
        <el-table-column prop="uid" label="用户" width="70" />
        <el-table-column label="流量" width="120">
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
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Connection, User, Switch } from '@element-plus/icons-vue'
import { api, fmtBytes } from '../api'

const status = ref({})
const users = ref([])
const loading = ref(false)
const loadingUsers = ref(false)
const kernelActing = ref('')
let timer = null

const nodes = computed(() => status.value.nodes || [])
const running = computed(() => status.value.running || 0)
const onlineCount = computed(() => users.value.filter(u => u.ips && u.ips.length).length)
const totalTraffic = computed(() => users.value.reduce((s, u) => s + (u.traffic || 0), 0))

const lastSync = computed(() => {
  let t = ''
  nodes.value.forEach(n => { if (n.panel && n.panel.lastSync) t = n.panel.lastSync })
  return t
})

// 面板同步小标签
const syncBadgeClass = computed(() => {
  if (!nodes.value.length) return 'off'
  const hasErr = nodes.value.some(n => n.panel && n.panel.lastError)
  return hasErr ? 'err' : 'ok'
})
const syncBadgeText = computed(() => {
  if (!nodes.value.length) return '未配置'
  const hasErr = nodes.value.some(n => n.panel && n.panel.lastError)
  const t = lastSync.value
  return hasErr ? '错误' : ('正常' + (t ? ' ' + t.slice(11, 16) : ''))
})
const syncBadgeTitle = computed(() => {
  const errs = nodes.value.filter(n => n.panel && n.panel.lastError).map(n => n.name + ': ' + n.panel.lastError)
  return errs.join('\n')
})

// 内核总开关
const kernelRows = computed(() => {
  const enabled = nodes.value.filter(n => n.enabled)
  const total = enabled.length
  const summarize = (key) => {
    const dots = enabled.map(n => !!(n[key] && n[key].running))
    const on = dots.filter(Boolean).length
    let state, stateType
    if (on === 0) { state = '已停止'; stateType = 'danger' }
    else if (on === total) { state = '运行中'; stateType = 'success' }
    else { state = on + '/' + total + ' 运行'; stateType = 'warning' }
    return { dots, state, stateType }
  }
  return [
    { name: 'Xray', action: 'xray', ...summarize('xray') },
    { name: 'Hysteria2', action: 'hy2', ...summarize('hy2') }
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

const PROTO_LABELS = { vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'SS', hysteria2: 'Hysteria2' }
const protoLabel = (p) => PROTO_LABELS[p] || p || '未知'

function nodeState(row) {
  if (!row.enabled) return { text: '已禁用', type: 'info' }
  const ok = (row.xray && row.xray.running) || (row.hy2 && row.hy2.running)
  return ok ? { text: '正常', type: 'success' } : { text: '停止', type: 'danger' }
}
const nodeStateText = (row) => nodeState(row).text
const nodeStateType = (row) => nodeState(row).type

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
/* 系统状态仪表盘大框 */
.dash-card { background: #fff; border-radius: 8px; box-shadow: 0 1px 4px rgba(0,0,0,.06); margin-bottom: 16px; overflow: hidden; }
.dash-body { display: flex; align-items: center; flex-wrap: wrap; }
.dash-item { flex: 1 1 0; text-align: center; padding: 18px 10px; min-width: 90px; }
.dash-num { font-size: 26px; font-weight: 700; color: #1d2129; }
.dash-num.ok { color: #67c23a; }
.dash-lbl { font-size: 12px; color: #909399; margin-top: 4px; }
.dash-sep { width: 1px; align-self: stretch; background: #f0f2f5; margin: 14px 0; }
.dash-sync { padding: 0 18px; color: #909399; font-size: 12px; white-space: nowrap; }
@media (max-width: 700px) {
  .dash-item { min-width: 70px; padding: 12px 6px; }
  .dash-num { font-size: 20px; }
  .dash-sync { width: 100%; text-align: center; border-top: 1px solid #f0f2f5; padding: 10px 0; }
}
/* 电脑端双列（内核开关 + 节点列表并排） */
.dual-col { display: grid; grid-template-columns: 1fr; gap: 16px; }
@media (min-width: 1400px) {
  .dual-col { grid-template-columns: 1fr 1fr 1fr; }
}
@media (min-width: 1200px) and (max-width: 1399px) {
  .dual-col { grid-template-columns: 1fr 1fr; }
}
/* 内核开关圆点 */
.kernel-dot { display: inline-block; width: 9px; height: 9px; border-radius: 50%; margin-right: 5px; background: #e4e7ed; }
.kernel-dot.on { background: #67c23a; }
.kernel-dot.off { background: #f56c6c; }
/* 面板同步小标签 */
.sync-badge { margin-left: 10px; font-size: 11px; font-weight: normal; }
.sync-badge.ok { color: #67c23a; }
.sync-badge.err { color: #f56c6c; }
.sync-badge.off { color: #909399; }
.offline { color: #c0c4cc; font-size: 12px; }
</style>
