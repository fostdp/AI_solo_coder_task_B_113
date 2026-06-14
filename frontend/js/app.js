const API_BASE = '';
let ws = null;
let currentBed = null;
let bedData = {};
let bedVitals = {};
let bedRisks = {};
let alerts = [];
let charts = {};
let miniChart = null;
let selectedBedForDetail = null;
let bedChart = null;
let riskHeatmap = null;

const tabInitialized = {};
let vapChart = null;
let resistanceGraph = null;

document.addEventListener('DOMContentLoaded', () => {
    initTabs();
    initBedChart();
    initCharts();
    initHeatmap();
    loadInitialData();
    connectWebSocket();
    updateTime();
    setInterval(updateTime, 1000);
    setInterval(loadStatistics, 5000);
    setTimeout(() => {
        initVapTab();
        initResistanceTab();
        initOptimizerTab();
        initTransportTab();
    }, 500);
});

function initTabs() {
    document.querySelectorAll('.tab-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            const tab = btn.dataset.tab;
            document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            btn.classList.add('active');
            document.getElementById('tab-' + tab).classList.add('active');

            if (tab === 'vitals' && charts.chartECG) {
                setTimeout(() => {
                    Object.values(charts).forEach(c => c && c.resize());
                }, 100);
            }
            if (tab === 'heatmap' && riskHeatmap) {
                setTimeout(() => {
                    riskHeatmap.resize();
                    loadHeatmapData();
                }, 100);
            }
            if (tab === 'vap') {
                if (!tabInitialized.vap) {
                    initVapTab();
                }
                setTimeout(() => {
                    if (vapChart) vapChart.resize();
                }, 100);
            }
            if (tab === 'resistance') {
                if (!tabInitialized.resistance) {
                    initResistanceTab();
                }
                setTimeout(() => {
                    if (resistanceGraph) resistanceGraph.resize();
                }, 100);
            }
            if (tab === 'optimizer' && !tabInitialized.optimizer) {
                initOptimizerTab();
            }
            if (tab === 'transport' && !tabInitialized.transport) {
                initTransportTab();
            }
        });
    });
}

function initBedChart() {
    bedChart = new BedChart('bedChartContainer');
    bedChart.onBedClick((bedId, bed) => {
        selectBed(bedId);
    });
}

function initHeatmap() {
    riskHeatmap = new RiskHeatmap('heatmapChart');
}

function selectBed(bedId) {
    selectedBedForDetail = bedId;
    const bed = bedData[bedId];
    if (!bed) return;

    document.getElementById('panelEmpty').style.display = 'none';
    document.getElementById('panelContent').style.display = 'block';

    document.getElementById('detailBedCode').textContent = bed.bed_code;
    document.getElementById('detailPatientName').textContent = bed.patient_name;
    document.getElementById('detailPatientAge').textContent = bed.patient_age;
    document.getElementById('detailPatientGender').textContent = bed.patient_gender;

    updateBedDetail(bedId);
    loadBedVitalsChart(bedId);
}

function updateBedDetail(bedId) {
    const vitals = bedVitals[bedId];
    const risk = bedRisks[bedId];

    if (vitals) {
        document.getElementById('vitalECG').textContent = vitals.ecg ? vitals.ecg.toFixed(1) : '--';
        document.getElementById('vitalVent').textContent = vitals.ventilator ? vitals.ventilator.toFixed(1) : '--';
        document.getElementById('vitalSpO2').textContent = vitals.spo2 ? vitals.spo2.toFixed(1) : '--';
        document.getElementById('vitalTemp').textContent = vitals.temperature ? vitals.temperature.toFixed(1) : '--';
    }

    if (risk) {
        document.getElementById('detailSOFA').textContent = risk.sofa_score.toFixed(1);
        document.getElementById('detailSepsis').textContent = (risk.sepsis_probability * 100).toFixed(1) + '%';
        document.getElementById('detailCRE').textContent = (risk.cre_risk * 100).toFixed(1) + '%';
        document.getElementById('detailMRSA').textContent = (risk.mrsa_risk * 100).toFixed(1) + '%';
    }
}

function initCharts() {
    charts.chartECG = echarts.init(document.getElementById('chartECG'), 'dark');
    charts.chartVent = echarts.init(document.getElementById('chartVent'), 'dark');
    charts.chartSpO2 = echarts.init(document.getElementById('chartSpO2'), 'dark');
    charts.chartTemp = echarts.init(document.getElementById('chartTemp'), 'dark');
    miniChart = echarts.init(document.getElementById('miniChart'), 'dark');

    const commonOption = (color, yMin, yMax) => ({
        backgroundColor: 'transparent',
        tooltip: { trigger: 'axis' },
        grid: { left: 50, right: 20, top: 20, bottom: 30 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 10 }
        },
        yAxis: {
            type: 'value',
            min: yMin,
            max: yMax,
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 10 },
            splitLine: { lineStyle: { color: 'rgba(42, 74, 122, 0.3)' } }
        },
        series: [{
            type: 'line',
            smooth: true,
            showSymbol: false,
            lineStyle: { color, width: 2 },
            areaStyle: {
                color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
                    { offset: 0, color: color + '66' },
                    { offset: 1, color: color + '00' }
                ])
            },
            data: []
        }]
    });

    charts.chartECG.setOption(commonOption('#ef4444', 40, 180));
    charts.chartVent.setOption(commonOption('#8b5cf6', 5, 40));
    charts.chartSpO2.setOption(commonOption('#22c55e', 70, 100));
    charts.chartTemp.setOption(commonOption('#f97316', 35, 42));

    miniChart.setOption({
        backgroundColor: 'transparent',
        tooltip: { trigger: 'axis' },
        legend: {
            data: ['心率', '血氧'],
            textStyle: { color: '#8b9bb4', fontSize: 10 },
            top: 0
        },
        grid: { left: 40, right: 15, top: 25, bottom: 25 },
        xAxis: {
            type: 'time',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 9 }
        },
        yAxis: {
            type: 'value',
            axisLine: { lineStyle: { color: '#2a4a7a' } },
            axisLabel: { color: '#8b9bb4', fontSize: 9 },
            splitLine: { lineStyle: { color: 'rgba(42, 74, 122, 0.3)' } }
        },
        series: [
            { name: '心率', type: 'line', smooth: true, showSymbol: false,
              lineStyle: { color: '#ef4444', width: 1.5 }, data: [] },
            { name: '血氧', type: 'line', smooth: true, showSymbol: false,
              lineStyle: { color: '#22c55e', width: 1.5 }, data: [], yAxisIndex: 0 }
        ]
    });

    document.getElementById('bedSelect').addEventListener('change', loadVitalsForSelectedBed);
    document.getElementById('timeRange').addEventListener('change', loadVitalsForSelectedBed);

    window.addEventListener('resize', () => {
        Object.values(charts).forEach(c => c && c.resize());
        if (miniChart) miniChart.resize();
        if (riskHeatmap) riskHeatmap.resize();
    });
}

function loadInitialData() {
    fetch(API_BASE + '/api/beds')
        .then(r => r.json())
        .then(data => {
            data.forEach(bed => {
                bedData[bed.id] = bed;
            });
            populateBedSelect(data);
            if (bedChart) {
                bedChart.setBeds(data);
            }
            if (riskHeatmap) {
                riskHeatmap.setBeds(data);
            }
        });

    loadStatistics();
    loadAlerts();
}

function populateBedSelect(beds) {
    const select = document.getElementById('bedSelect');
    beds.forEach(bed => {
        const opt = document.createElement('option');
        opt.value = bed.id;
        opt.textContent = `${bed.bed_code} - ${bed.patient_name}`;
        select.appendChild(opt);
    });
}

function loadStatistics() {
    fetch(API_BASE + '/api/statistics')
        .then(r => r.json())
        .then(s => {
            document.getElementById('totalBeds').textContent = s.total_beds;
            document.getElementById('occupiedBeds').textContent = s.occupied_beds;
            document.getElementById('activeAlerts').textContent = s.active_alerts;
            document.getElementById('highRiskSepsis').textContent = s.high_risk_sepsis;
            document.getElementById('highRiskInfection').textContent = s.high_risk_infection;
            document.getElementById('avgSOFA').textContent = s.avg_sofa_score.toFixed(1);
        });
}

function loadAlerts() {
    fetch(API_BASE + '/api/alerts/active')
        .then(r => r.json())
        .then(data => {
            alerts = data;
            renderAlerts();
        });
}

function renderAlerts() {
    const filter = document.querySelector('.filter-btn.active')?.dataset.filter || 'all';
    const list = document.getElementById('alertsList');

    let filtered = alerts;
    if (filter === 'unack') {
        filtered = alerts.filter(a => !a.acknowledged);
    } else if (filter !== 'all') {
        filtered = alerts.filter(a => a.alert_type === filter);
    }

    if (filtered.length === 0) {
        list.innerHTML = '<div class="alerts-empty">暂无告警</div>';
        return;
    }

    list.innerHTML = filtered.map(alert => {
        const icons = {
            sepsis: '🚨',
            cre_infection: '🦠',
            mrsa_infection: '🧫'
        };
        const types = {
            sepsis: '脓毒症预警',
            cre_infection: 'CRE感染风险',
            mrsa_infection: 'MRSA感染风险'
        };
        return `
        <div class="alert-item severity-${alert.severity} ${alert.acknowledged ? 'acknowledged' : ''}">
            <div class="alert-icon">${icons[alert.alert_type] || '⚠️'}</div>
            <div class="alert-main">
                <div class="alert-type">${types[alert.alert_type] || alert.alert_type} · ${alert.severity.toUpperCase()}</div>
                <div class="alert-message">${alert.message}</div>
            </div>
            <div class="alert-meta">
                <div class="alert-bed">ICU-${String(alert.bed_id).padStart(3, '0')}</div>
                <div class="alert-value">触发值: ${alert.trigger_value.toFixed(2)} / 阈值: ${alert.threshold}</div>
                <div>${new Date(alert.created_at).toLocaleString('zh-CN')}</div>
            </div>
            <div class="alert-actions">
                ${!alert.acknowledged ? `<button class="btn btn-primary" style="font-size:11px;padding:5px 10px;" onclick="acknowledgeAlert(${alert.id})">确认</button>` : `<span style="color:#22c55e;font-size:11px;">已确认</span>`}
            </div>
        </div>`;
    }).join('');

    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.onclick = () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            renderAlerts();
        };
    });
}

function acknowledgeAlert(id) {
    fetch(API_BASE + `/api/alerts/${id}/acknowledge`, { method: 'POST' })
        .then(r => r.json())
        .then(() => loadAlerts());
}

function loadVitalsForSelectedBed() {
    const bedId = document.getElementById('bedSelect').value;
    if (!bedId) return;

    const seconds = document.getElementById('timeRange').value;
    fetch(API_BASE + `/api/beds/${bedId}/vitals/recent?seconds=${seconds}`)
        .then(r => r.json())
        .then(data => {
            updateChart(charts.chartECG, data.ecg || []);
            updateChart(charts.chartVent, data.ventilator || []);
            updateChart(charts.chartSpO2, data.spo2 || []);
            updateChart(charts.chartTemp, data.temperature || []);
        });
}

function updateChart(chart, points) {
    const data = points.map(p => [new Date(p.time).getTime(), p.value]);
    chart.setOption({ series: [{ data }] });
}

function loadBedVitalsChart(bedId) {
    fetch(API_BASE + `/api/beds/${bedId}/vitals/recent?seconds=300`)
        .then(r => r.json())
        .then(data => {
            const ecgData = (data.ecg || []).map(p => [new Date(p.time).getTime(), p.value]);
            const spo2Data = (data.spo2 || []).map(p => [new Date(p.time).getTime(), p.value]);
            miniChart.setOption({
                series: [
                    { data: ecgData },
                    { data: spo2Data }
                ]
            });
        });
}

function loadHeatmapData() {
    fetch(API_BASE + '/api/infection/risk')
        .then(r => r.json())
        .then(data => {
            if (riskHeatmap) {
                riskHeatmap.setRiskData(data);
            }
        });
}

function connectWebSocket() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${location.host}/ws`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
        document.getElementById('connectionStatus').textContent = '● 已连接';
        document.getElementById('connectionStatus').className = 'status-badge connected';
    };

    ws.onclose = () => {
        document.getElementById('connectionStatus').textContent = '● 已断开';
        document.getElementById('connectionStatus').className = 'status-badge disconnected';
        setTimeout(connectWebSocket, 3000);
    };

    ws.onerror = () => {
        document.getElementById('connectionStatus').textContent = '● 连接错误';
        document.getElementById('connectionStatus').className = 'status-badge disconnected';
    };

    ws.onmessage = (e) => {
        try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'vitals_update') {
                handleVitalsUpdate(msg.data);
            } else if (msg.type === 'alert') {
                handleNewAlert(msg.data);
            } else if (msg.type === 'vap_risk_alert') {
                handleVapRiskAlert(msg.data);
            } else if (msg.type === 'resistance_alert') {
                handleResistanceAlert(msg.data);
            } else if (msg.type === 'optimizer_suggestion') {
                handleOptimizerSuggestion(msg.data);
            } else if (msg.type === 'transport_risk_alert') {
                handleTransportRiskAlert(msg.data);
            }
        } catch (err) {
            console.error('WS解析错误:', err);
        }
    };
}

function handleVitalsUpdate(data) {
    data.forEach(item => {
        const bedId = item.bed.id;
        if (item.vitals) bedVitals[bedId] = item.vitals;
        if (item.risk) bedRisks[bedId] = item.risk;
    });

    if (bedChart) {
        bedChart.setVitals(bedVitals);
        bedChart.setRisks(bedRisks);
    }

    if (selectedBedForDetail) {
        updateBedDetail(selectedBedForDetail);
    }
}

function handleNewAlert(alert) {
    alerts.unshift(alert);
    renderAlerts();
    loadStatistics();

    const toast = document.getElementById('alertToast');
    const typeNames = {
        sepsis: '脓毒症预警',
        cre_infection: 'CRE感染风险',
        mrsa_infection: 'MRSA感染风险'
    };
    toast.innerHTML = `
        <h4>⚠️ ${typeNames[alert.alert_type] || '告警'} - 床位ICU-${String(alert.bed_id).padStart(3, '0')}</h4>
        <p>${alert.message}</p>
    `;
    toast.classList.remove('hidden');
    setTimeout(() => toast.classList.add('hidden'), 5000);
}

function updateTime() {
    document.getElementById('currentTime').textContent = new Date().toLocaleString('zh-CN');
}

function showDetailVitals() {
    if (selectedBedForDetail) {
        document.querySelector('.tab-btn[data-tab="vitals"]').click();
        document.getElementById('bedSelect').value = selectedBedForDetail;
        loadVitalsForSelectedBed();
    }
}

function recordProcedure() {
    if (!selectedBedForDetail) return;
    const procType = prompt('请输入操作类型（如：气管插管、中心静脉置管等）:');
    if (!procType) return;

    fetch(API_BASE + `/api/beds/${selectedBedForDetail}/invasive`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            bed_id: selectedBedForDetail,
            procedure_type: procType,
            procedure_time: new Date().toISOString(),
            notes: '前端记录'
        })
    }).then(r => r.json()).then(d => alert('操作已记录'));
}

function recordAntibiotic() {
    if (!selectedBedForDetail) return;
    const abType = prompt('请输入抗生素类型（如：美罗培南、万古霉素等）:');
    if (!abType) return;
    const dosage = parseFloat(prompt('请输入剂量(g):', '1.0') || '1');

    fetch(API_BASE + `/api/beds/${selectedBedForDetail}/antibiotics`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            bed_id: selectedBedForDetail,
            antibiotic_type: abType,
            dosage: dosage,
            start_date: new Date().toISOString(),
            end_date: new Date(Date.now() + 7 * 86400000).toISOString()
        })
    }).then(r => r.json()).then(d => alert('抗生素已记录'));
}

function showToast(title, message, type = 'warning') {
    const toast = document.getElementById('alertToast');
    const colors = { warning: '#f59e0b', danger: '#ef4444', info: '#3b82f6', success: '#10b981' };
    const icons = { warning: '⚠️', danger: '🚨', info: 'ℹ️', success: '✅' };
    toast.style.borderColor = colors[type] || colors.warning;
    toast.innerHTML = `
        <h4 style="color:${colors[type] || colors.warning}">${icons[type] || '⚠️'} ${title}</h4>
        <p>${message}</p>
    `;
    toast.classList.remove('hidden');
    setTimeout(() => toast.classList.add('hidden'), 5000);
}

function handleVapRiskAlert(data) {
    const banner = document.getElementById('vap-alert-banner');
    if (banner) {
        banner.style.display = 'block';
        banner.style.background = 'rgba(239, 68, 68, 0.15)';
        banner.style.border = '1px solid #ef4444';
        banner.style.borderRadius = '8px';
        banner.style.padding = '12px 16px';
        banner.style.marginBottom = '16px';
        banner.innerHTML = `
            <div style="display:flex;align-items:center;gap:10px;">
                <span style="font-size:20px;">⚠️</span>
                <div>
                    <strong style="color:#ef4444;">VAP高风险预警</strong>
                    <span style="color:#e0e6ed;margin-left:12px;">床位ICU-${String(data.bed_id).padStart(3, '0')} 风险值 ${(data.vap_risk * 100).toFixed(1)}%</span>
                </div>
            </div>
        `;
    }
    showToast('VAP高风险预警', `床位ICU-${String(data.bed_id).padStart(3, '0')} VAP风险达 ${(data.vap_risk * 100).toFixed(1)}%，建议加强气道护理`, 'danger');
}

function handleResistanceAlert(data) {
    if (resistanceGraph) {
        const option = resistanceGraph.getOption();
        if (option && option.series && option.series[0] && option.series[0].data) {
            option.series[0].data.forEach(node => {
                if (data.path_nodes && data.path_nodes.includes(node.id)) {
                    node.itemStyle = node.itemStyle || {};
                    node.itemStyle.borderColor = '#ef4444';
                    node.itemStyle.borderWidth = 3;
                    node.label = node.label || {};
                    node.label.color = '#ef4444';
                    node.label.fontWeight = 'bold';
                }
            });
            if (option.series[0].links) {
                option.series[0].links.forEach(link => {
                    if (data.path_edges) {
                        const edgeKey = `${link.source}-${link.target}`;
                        const reverseKey = `${link.target}-${link.source}`;
                        if (data.path_edges.some(e => (e.source === link.source && e.target === link.target) || (e.source === link.target && e.target === link.source))) {
                            link.lineStyle = link.lineStyle || {};
                            link.lineStyle.color = '#ef4444';
                            link.lineStyle.width = 3;
                        }
                    }
                });
            }
            resistanceGraph.setOption(option);
        }
    }
    showToast('耐药传播预警', `检测到${data.bacteria || '细菌'}潜在传播路径，涉及 ${data.path_nodes ? data.path_nodes.length : 0} 个床位节点`, 'danger');
}

function handleOptimizerSuggestion(data) {
    addSuggestionCard(data);
}

function handleTransportRiskAlert(data) {
    showToast('转运风险通知', `床位ICU-${String(data.bed_id).padStart(3, '0')} 转运风险等级: ${data.risk_level || '未知'}，评分: ${(data.risk_score * 100).toFixed(0)}`, data.risk_level === 'high' ? 'danger' : 'warning');
}

function getRiskColor(level) {
    if (level === 'high' || level === 'critical') return '#ef4444';
    if (level === 'medium' || level === 'warning') return '#f59e0b';
    return '#10b981';
}

function getRiskLevel(risk) {
    if (risk >= 0.7) return 'high';
    if (risk >= 0.4) return 'medium';
    return 'low';
}

function getRiskLevelText(level) {
    const texts = { high: '高风险', medium: '中风险', low: '低风险', critical: '极高' };
    return texts[level] || level;
}

function initVapTab() {
    if (tabInitialized.vap) return;
    tabInitialized.vap = true;

    vapChart = echarts.init(document.getElementById('vap-chart'), 'dark');

    fetch(API_BASE + '/api/vap')
        .then(r => r.json())
        .then(data => {
            const tbody = document.querySelector('#vap-table tbody');
            if (tbody && Array.isArray(data)) {
                tbody.innerHTML = data.map(item => {
                    const level = getRiskLevel(item.vap_risk || 0);
                    const color = getRiskColor(level);
                    const factors = item.risk_factors ? item.risk_factors.join(', ') : '-';
                    return `
                        <tr style="border-bottom:1px solid #1e3a5f;">
                            <td style="padding:10px 12px;color:#3b82f6;font-weight:600;">${item.bed_code || 'ICU-' + String(item.bed_id).padStart(3, '0')}</td>
                            <td style="padding:10px 12px;">${item.patient_name || '-'}</td>
                            <td style="padding:10px 12px;text-align:center;">${item.vent_days || 0}天</td>
                            <td style="padding:10px 12px;text-align:center;font-weight:700;color:${color};">${((item.vap_risk || 0) * 100).toFixed(1)}%</td>
                            <td style="padding:10px 12px;text-align:center;"><span style="padding:3px 10px;border-radius:12px;background:${color}22;color:${color};font-size:12px;font-weight:600;">${getRiskLevelText(level)}</span></td>
                            <td style="padding:10px 12px;color:#8b9bb4;font-size:12px;">${factors}</td>
                        </tr>
                    `;
                }).join('');
            }

            const bedCodes = data.map(d => d.bed_code || 'ICU-' + String(d.bed_id).padStart(3, '0'));
            const risks = data.map(d => ((d.vap_risk || 0) * 100).toFixed(1));
            const colors = data.map(d => getRiskColor(getRiskLevel(d.vap_risk || 0)));

            vapChart.setOption({
                backgroundColor: 'transparent',
                tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
                grid: { left: 50, right: 20, top: 30, bottom: 80 },
                xAxis: {
                    type: 'category',
                    data: bedCodes,
                    axisLabel: { rotate: 45, color: '#8b9bb4', fontSize: 10 },
                    axisLine: { lineStyle: { color: '#2a4a7a' } }
                },
                yAxis: {
                    type: 'value',
                    max: 100,
                    name: '风险值(%)',
                    nameTextStyle: { color: '#8b9bb4' },
                    axisLabel: { color: '#8b9bb4' },
                    splitLine: { lineStyle: { color: 'rgba(42, 74, 122, 0.3)' } },
                    axisLine: { lineStyle: { color: '#2a4a7a' } }
                },
                series: [{
                    type: 'bar',
                    data: risks.map((v, i) => ({
                        value: parseFloat(v),
                        itemStyle: { color: colors[i], borderRadius: [4, 4, 0, 0] }
                    })),
                    barWidth: '60%',
                    markLine: {
                        silent: true,
                        lineStyle: { color: '#ef4444', type: 'dashed' },
                        data: [{ yAxis: 70, label: { formatter: '高风险线 70%', color: '#ef4444' } }]
                    }
                }]
            });
        })
        .catch(err => {
            console.error('VAP数据加载失败:', err);
        });
}

function initResistanceTab() {
    if (tabInitialized.resistance) return;
    tabInitialized.resistance = true;

    resistanceGraph = echarts.init(document.getElementById('resistance-graph'), 'dark');

    fetch(API_BASE + '/api/resistance/heatmap')
        .then(r => r.json())
        .then(data => {
            const nodes = [];
            const edges = [];

            if (data.nodes && Array.isArray(data.nodes)) {
                data.nodes.forEach(n => {
                    const risk = n.resistance_risk || n.risk || 0;
                    const level = getRiskLevel(risk);
                    nodes.push({
                        id: n.id || n.bed_id,
                        name: n.bed_code || ('ICU-' + String(n.bed_id || n.id).padStart(3, '0')),
                        symbolSize: 30 + risk * 30,
                        category: level,
                        itemStyle: { color: getRiskColor(level) },
                        value: (risk * 100).toFixed(1)
                    });
                });
            }

            if (data.edges && Array.isArray(data.edges)) {
                data.edges.forEach(e => {
                    const weight = e.transmission_prob || e.weight || 0.5;
                    edges.push({
                        source: e.source,
                        target: e.target,
                        value: weight,
                        lineStyle: {
                            width: Math.max(1, weight * 5),
                            color: weight > 0.6 ? '#ef4444' : weight > 0.3 ? '#f59e0b' : '#4a6a9a',
                            curveness: 0.2
                        }
                    });
                });
            }

            resistanceGraph.setOption({
                backgroundColor: 'transparent',
                tooltip: {
                    formatter: (params) => {
                        if (params.dataType === 'node') {
                            return `<strong>${params.name}</strong><br/>耐药风险: ${params.data.value}%`;
                        } else if (params.dataType === 'edge') {
                            return `${params.data.source} → ${params.data.target}<br/>传播概率: ${(params.data.value * 100).toFixed(1)}%`;
                        }
                        return '';
                    }
                },
                legend: {
                    data: [
                        { name: 'high', itemStyle: { color: '#ef4444' } },
                        { name: 'medium', itemStyle: { color: '#f59e0b' } },
                        { name: 'low', itemStyle: { color: '#10b981' } }
                    ],
                    textStyle: { color: '#8b9bb4' },
                    formatter: (name) => getRiskLevelText(name),
                    top: 10
                },
                animationDurationUpdate: 1500,
                animationEasingUpdate: 'quinticInOut',
                series: [{
                    type: 'graph',
                    layout: 'force',
                    data: nodes,
                    links: edges,
                    categories: [
                        { name: 'high' },
                        { name: 'medium' },
                        { name: 'low' }
                    ],
                    roam: true,
                    draggable: true,
                    label: { show: true, color: '#fff', fontSize: 11, position: 'right' },
                    edgeLabel: {
                        show: true,
                        formatter: (p) => (p.data.value * 100).toFixed(0) + '%',
                        color: '#8b9bb4',
                        fontSize: 10
                    },
                    force: {
                        repulsion: 400,
                        edgeLength: [80, 200],
                        gravity: 0.1
                    },
                    lineStyle: { opacity: 0.8 },
                    emphasis: {
                        focus: 'adjacency',
                        lineStyle: { width: 5 }
                    }
                }]
            });

            resistanceGraph.on('click', (params) => {
                if (params.dataType === 'node') {
                    const panel = document.getElementById('resistance-panel-content');
                    const node = data.nodes?.find(n => (n.id || n.bed_id) === params.data.id);
                    panel.innerHTML = `
                        <div style="margin-bottom:16px;padding-bottom:16px;border-bottom:1px solid #1e3a5f;">
                            <h4 style="color:${getRiskColor(getRiskLevel(node?.resistance_risk || 0))};margin-bottom:8px;">${params.name}</h4>
                            <div style="font-size:13px;color:#8b9bb4;line-height:1.8;">
                                <div>耐药风险值: <strong style="color:#fff;">${params.data.value}%</strong></div>
                                <div>风险等级: <strong style="color:${getRiskColor(getRiskLevel(node?.resistance_risk || 0))};">${getRiskLevelText(getRiskLevel(node?.resistance_risk || 0))}</strong></div>
                                <div>主要耐药菌: <strong style="color:#fff;">${node?.bacteria || 'CRE/MRSA'}</strong></div>
                            </div>
                        </div>
                        <div>
                            <h5 style="color:#fff;margin-bottom:10px;">关联传播路径</h5>
                            <div style="font-size:12px;color:#8b9bb4;line-height:1.8;">
                                ${data.edges?.filter(e => e.source === params.data.id || e.target === params.data.id).map(e => {
                                    const other = e.source === params.data.id ? e.target : e.source;
                                    const dir = e.source === params.data.id ? '→' : '←';
                                    return `<div>• ${dir} ICU-${String(other).padStart(3, '0')} (${((e.transmission_prob || e.weight) * 100).toFixed(1)}%)</div>`;
                                }).join('') || '<div style="color:#4a6a9a;">无关联路径</div>'}
                            </div>
                        </div>
                    `;
                }
            });
        })
        .catch(err => {
            console.error('耐药图谱数据加载失败:', err);
        });
}

function addSuggestionCard(suggestion) {
    const list = document.getElementById('suggestion-list');
    if (!list) return;

    const level = suggestion.severity || suggestion.level || 'low';
    const card = document.createElement('div');
    card.className = `suggestion-card suggestion-${level}`;
    card.style.background = 'rgba(30, 58, 100, 0.4)';
    card.innerHTML = `
        <div style="display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:6px;">
            <strong style="color:#fff;font-size:13px;">${suggestion.title || suggestion.type || '建议'}</strong>
            <span style="font-size:10px;color:#8b9bb4;">${new Date(suggestion.created_at || Date.now()).toLocaleTimeString('zh-CN')}</span>
        </div>
        <p style="font-size:12px;color:#c0ccdb;line-height:1.5;margin:0;">${suggestion.message || suggestion.description || ''}</p>
        ${suggestion.action ? `<button class="btn btn-primary" style="margin-top:8px;padding:5px 12px;font-size:11px;" onclick="this.parentElement.remove()">采纳建议</button>` : ''}
    `;
    list.insertBefore(card, list.firstChild);
}

function initOptimizerTab() {
    if (tabInitialized.optimizer) return;
    tabInitialized.optimizer = true;

    fetch(API_BASE + '/api/optimizer/solution')
        .then(r => r.json())
        .then(data => {
            const roomGrid = document.getElementById('room-assign-grid');
            if (roomGrid && data.room_assignments) {
                roomGrid.innerHTML = data.room_assignments.map(room => {
                    const statusColor = room.status === 'occupied' ? '#ef4444' : room.status === 'reserved' ? '#f59e0b' : '#10b981';
                    const statusText = room.status === 'occupied' ? '使用中' : room.status === 'reserved' ? '预留' : '空闲';
                    return `
                        <div style="background:${statusColor}22;border:2px solid ${statusColor};border-radius:8px;padding:12px;text-align:center;">
                            <div style="font-weight:700;color:#fff;font-size:14px;">${room.room_code || room.id}</div>
                            <div style="font-size:11px;color:${statusColor};margin-top:4px;">${room.type || '负压病房'}</div>
                            <div style="font-size:11px;color:#c0ccdb;margin-top:4px;">${room.bed_code || statusText}</div>
                        </div>
                    `;
                }).join('');
            }

            const scheduleTbody = document.querySelector('#nurse-schedule-table tbody');
            if (scheduleTbody && data.nurse_schedules) {
                scheduleTbody.innerHTML = data.nurse_schedules.map(ns => {
                    const loadColor = ns.workload >= 0.8 ? '#ef4444' : ns.workload >= 0.6 ? '#f59e0b' : '#10b981';
                    const statusText = ns.status === 'on_duty' ? '在岗' : ns.status === 'rest' ? '休息' : '待命';
                    return `
                        <tr style="border-bottom:1px solid #1e3a5f;">
                            <td style="padding:10px 12px;color:#fff;font-weight:600;">${ns.nurse_name || ns.nurse_id}</td>
                            <td style="padding:10px 12px;color:#8b9bb4;font-size:12px;">${ns.assigned_beds ? ns.assigned_beds.join(', ') : '-'}</td>
                            <td style="padding:10px 12px;text-align:center;">${ns.shift || '-'}</td>
                            <td style="padding:10px 12px;">
                                <div class="risk-bar" style="height:8px;">
                                    <div class="risk-bar-fill" style="width:${(ns.workload || 0) * 100}%;background:${loadColor};"></div>
                                </div>
                                <span style="font-size:11px;color:${loadColor};display:block;text-align:center;margin-top:2px;">${((ns.workload || 0) * 100).toFixed(0)}%</span>
                            </td>
                            <td style="padding:10px 12px;text-align:center;"><span style="padding:2px 8px;border-radius:10px;font-size:11px;background:${ns.status === 'on_duty' ? '#3b82f622' : '#10b98122'};color:${ns.status === 'on_duty' ? '#3b82f6' : '#10b981'};">${statusText}</span></td>
                        </tr>
                    `;
                }).join('');
            }
        })
        .catch(err => {
            console.error('调度方案加载失败:', err);
        });

    fetch(API_BASE + '/api/optimizer/suggestions')
        .then(r => r.json())
        .then(data => {
            if (Array.isArray(data)) {
                data.forEach(s => addSuggestionCard(s));
            }
        })
        .catch(err => {
            console.error('调度建议加载失败:', err);
        });
}

function initTransportTab() {
    if (tabInitialized.transport) return;
    tabInitialized.transport = true;

    const form = document.getElementById('transport-form');
    if (form) {
        form.addEventListener('submit', (e) => {
            e.preventDefault();
            const formData = new FormData(form);
            const payload = {
                from_bed: parseInt(formData.get('from_bed')),
                to_bed: formData.get('to_bed'),
                distance: parseFloat(formData.get('distance')),
                priority: formData.get('priority'),
                is_emergency: formData.get('is_emergency') === 'on'
            };

            const submitBtn = form.querySelector('button[type="submit"]');
            submitBtn.disabled = true;
            submitBtn.textContent = '评估中...';

            fetch(API_BASE + '/api/transport/evaluate', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            })
                .then(r => r.json())
                .then(result => {
                    const resultDiv = document.getElementById('transport-result');
                    resultDiv.style.display = 'block';

                    const riskScore = result.risk_score || 0;
                    const level = getRiskLevel(riskScore);
                    const color = getRiskColor(level);

                    document.getElementById('transport-risk-score').textContent = (riskScore * 100).toFixed(1) + ' 分';
                    document.getElementById('transport-risk-bar-fill').style.width = (riskScore * 100) + '%';
                    document.getElementById('transport-risk-bar-fill').style.background = color;

                    const badge = document.getElementById('transport-risk-badge');
                    badge.textContent = getRiskLevelText(level);
                    badge.style.display = 'inline-block';
                    badge.style.padding = '6px 20px';
                    badge.style.borderRadius = '20px';
                    badge.style.fontWeight = '700';
                    badge.style.background = color + '22';
                    badge.style.color = color;
                    badge.style.border = `2px solid ${color}`;

                    const recList = document.getElementById('transport-recommendations');
                    const recs = result.recommendations || [];
                    recList.innerHTML = recs.map(r => `
                        <li style="padding:10px 12px;background:rgba(30, 58, 100, 0.4);border-radius:6px;margin-bottom:6px;border-left:3px solid ${r.level ? getRiskColor(r.level) : '#3b82f6'};">
                            <span style="color:#fff;font-size:13px;">${r.text || r.message || r}</span>
                        </li>
                    `).join('');

                    resultDiv.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
                })
                .catch(err => {
                    console.error('转运评估失败:', err);
                    alert('评估失败: ' + err.message);
                })
                .finally(() => {
                    submitBtn.disabled = false;
                    submitBtn.textContent = '评估转运风险';
                });
        });
    }
}

window.addEventListener('resize', () => {
    if (vapChart) vapChart.resize();
    if (resistanceGraph) resistanceGraph.resize();
});
