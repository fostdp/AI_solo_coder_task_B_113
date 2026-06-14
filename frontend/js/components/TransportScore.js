(function (global) {
    'use strict';

    /**
     * 转运风险评分仪表盘组件
     * 展示风险分(0-100)、等级、不良事件概率、特征贡献、推荐建议、GPS丢失指示
     */
    function TransportScore(containerId, options) {
        this.container = document.getElementById(containerId);
        if (!this.container) throw new Error('TransportScore: DOM #' + containerId + ' not found');
        this.options = options || {};
        this.gaugeChart = null;
        this._render();
        this._initGauge();
        var self = this;
        this._resizeHandler = function () { self.resize(); };
        window.addEventListener('resize', this._resizeHandler);
    }

    TransportScore.prototype._render = function () {
        this.container.innerHTML =
            '<div class="transport-score-wrapper" style="display:grid;grid-template-columns:1fr 1fr;gap:20px">' +
                '<div class="ts-gauge-panel" style="grid-column:1/3;padding:20px;border-radius:12px;border:1px solid #3f5175;background:rgba(0,0,0,0.2)">' +
                    '<div id="tsGauge" style="width:100%;height:320px"></div>' +
                '</div>' +
                '<div class="ts-info-panel" style="padding:16px;border-radius:12px;border:1px solid #3f5175;background:rgba(0,0,0,0.2)">' +
                    '<h4 style="margin:0 0 14px 0;color:#fff;padding-bottom:10px;border-bottom:1px solid #3f5175">' +
                        '<i class="fas fa-exclamation-triangle"></i> 风险详情' +
                    '</h4>' +
                    '<div id="tsLevelBadge" style="margin-bottom:14px"></div>' +
                    '<div id="tsMetaList" style="display:flex;flex-direction:column;gap:10px"></div>' +
                '</div>' +
                '<div class="ts-rec-panel" style="padding:16px;border-radius:12px;border:1px solid #3f5175;background:rgba(0,0,0,0.2)">' +
                    '<h4 style="margin:0 0 14px 0;color:#fff;padding-bottom:10px;border-bottom:1px solid #3f5175">' +
                        '<i class="fas fa-lightbulb"></i> 转运建议' +
                    '</h4>' +
                    '<div id="tsRecommendations" style="display:flex;flex-direction:column;gap:8px"></div>' +
                '</div>' +
                '<div class="ts-feature-panel" style="grid-column:1/3;padding:16px;border-radius:12px;border:1px solid #3f5175;background:rgba(0,0,0,0.2)">' +
                    '<h4 style="margin:0 0 14px 0;color:#fff;padding-bottom:10px;border-bottom:1px solid #3f5175">' +
                        '<i class="fas fa-chart-pie"></i> 特征贡献分析' +
                    '</h4>' +
                    '<div id="tsFeatures" style="display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:10px"></div>' +
                '</div>' +
            '</div>';
    };

    TransportScore.prototype._initGauge = function () {
        var dom = document.getElementById('tsGauge');
        if (!dom) return;
        this.gaugeChart = echarts.init(dom, 'dark');
        this.gaugeChart.setOption({
            backgroundColor: 'transparent',
            series: [{
                type: 'gauge',
                startAngle: 210,
                endAngle: -30,
                min: 0,
                max: 100,
                splitNumber: 10,
                radius: '85%',
                progress: { show: true, width: 28 },
                axisLine: {
                    lineStyle: {
                        width: 28,
                        color: [
                            [0.3, '#66bb6a'],
                            [0.6, '#ffb74d'],
                            [0.8, '#ff9800'],
                            [1, '#ef5350']
                        ]
                    }
                },
                pointer: {
                    length: '65%',
                    width: 8,
                    itemStyle: { color: '#fff' }
                },
                axisTick: {
                    distance: -36,
                    length: 8,
                    lineStyle: { color: '#fff', width: 2 }
                },
                splitLine: {
                    distance: -40,
                    length: 16,
                    lineStyle: { color: '#fff', width: 3 }
                },
                axisLabel: {
                    color: '#ccc',
                    distance: 16,
                    fontSize: 12
                },
                anchor: {
                    show: true,
                    size: 24,
                    itemStyle: {
                        color: '#fff',
                        borderWidth: 8,
                        borderColor: '#333'
                    }
                },
                title: {
                    offsetCenter: [0, '72%'],
                    fontSize: 16,
                    color: '#fff'
                },
                detail: {
                    valueAnimation: true,
                    offsetCenter: [0, '35%'],
                    fontSize: 60,
                    fontWeight: 'bold',
                    formatter: function (v) { return v.toFixed(0); },
                    color: '#fff'
                },
                data: [{ value: 50, name: '转运风险评分' }]
            }]
        });
    };

    TransportScore.prototype.setData = function (result) {
        var r = result || {};
        var score = r.riskScore !== undefined && r.riskScore !== null ? Number(r.riskScore) : 50;
        score = Math.max(0, Math.min(100, score));

        if (this.gaugeChart) {
            this.gaugeChart.setOption({
                series: [{ data: [{ value: score, name: '转运风险评分' }] }]
            });
        }

        var levelStyles = {
            low:      { cls: '#66bb6a', bg: 'rgba(102,187,106,0.15)', label: '低风险',   icon: '✓' },
            medium:   { cls: '#ffb74d', bg: 'rgba(255,183,77,0.15)',  label: '中风险',   icon: '⚠' },
            high:     { cls: '#ff9800', bg: 'rgba(255,152,0,0.15)',  label: '高风险',   icon: '❗' },
            critical: { cls: '#ef5350', bg: 'rgba(239,83,80,0.15)',  label: '极高危',   icon: '🚨' }
        };
        var levelKey = r.riskLevel && levelStyles[r.riskLevel] ? r.riskLevel : 'medium';
        var ls = levelStyles[levelKey];

        document.getElementById('tsLevelBadge').innerHTML =
            '<div style="padding:14px 18px;border-radius:10px;background:' + ls.bg + ';border:1px solid ' + ls.cls + '55">' +
                '<div style="display:flex;align-items:center;gap:12px">' +
                    '<span style="font-size:32px">' + ls.icon + '</span>' +
                    '<div>' +
                        '<div style="font-size:22px;font-weight:bold;color:' + ls.cls + '">' + ls.label + '</div>' +
                        '<div style="font-size:12px;color:#aaa;margin-top:2px">Risk Level: ' + levelKey + '</div>' +
                    '</div>' +
                '</div>' +
            '</div>';

        var adverseProb = r.adverseEventProb !== undefined ? Number(r.adverseEventProb) : 0;
        var metaItems = [
            { label: '不良事件概率', value: (adverseProb * 100).toFixed(1) + '%', cls: '#ef5350' },
            { label: '评分值', value: score + ' / 100', cls: '#fff' },
            { label: '请求ID', value: '#' + (r.requestId || 0), cls: '#aaa' }
        ];
        if (r.usedDefaultDistance) {
            metaItems.push({ label: '📍 GPS', value: '丢失，使用默认1000m', cls: '#ff9800' });
        }
        if (r.usedDefaultVitals) {
            metaItems.push({ label: '❤️ 体征', value: '缺失，使用默认值', cls: '#ff9800' });
        }

        document.getElementById('tsMetaList').innerHTML = metaItems.map(function (m) {
            return '<div style="display:flex;justify-content:space-between;padding:8px 12px;background:rgba(255,255,255,0.03);border-radius:6px">' +
                '<span style="color:#aaa">' + m.label + '</span>' +
                '<span style="font-weight:bold;color:' + m.cls + '">' + m.value + '</span>' +
            '</div>';
        }).join('');

        var recs = r.recommendations || [];
        var recsHtml = '';
        if (recs.length > 0) {
            recsHtml = recs.map(function (rec) {
                return '<div style="padding:10px 14px;border-radius:6px;background:rgba(100,181,246,0.08);border-left:3px solid #64b5f6;color:#b3d4fc;font-size:13px">' +
                    '<b style="color:#64b5f6">▸</b> ' + rec +
                '</div>';
            }).join('');
        } else {
            recsHtml = '<div style="padding:16px;text-align:center;color:#66bb6a;background:rgba(102,187,106,0.05);border-radius:6px">✓ 按标准流程转运即可</div>';
        }
        document.getElementById('tsRecommendations').innerHTML = recsHtml;

        var feats = r.featureContrib || {};
        var featNames = {
            vital_stability:   { name: '生命体征稳定', icon: '❤️' },
            infection_risk:    { name: '感染风险', icon: '🦠' },
            distance:          { name: '转运距离', icon: '📍' },
            urgent:            { name: '紧急程度', icon: '🚨' },
            priority:          { name: '优先级', icon: '⭐' },
            day_hours:         { name: '时段因素', icon: '🕐' },
            distance_norm:     { name: '转运距离(归一)', icon: '📍' }
        };
        var featEntries = Object.entries(feats);
        var total = 0;
        featEntries.forEach(function (e) { total += Number(e[1]) || 0; });
        if (total <= 0) total = 1;

        var featsHtml = featEntries.map(function (entry) {
            var k = entry[0];
            var v = Number(entry[1]) || 0;
            var fn = featNames[k] || { name: k, icon: '📊' };
            var pct = Math.round((v / total) * 100);
            if (isNaN(pct)) pct = 0;
            return '<div style="padding:12px;border-radius:8px;background:rgba(255,255,255,0.03);border:1px solid #333">' +
                '<div style="display:flex;justify-content:space-between;margin-bottom:8px">' +
                    '<span style="color:#fff">' + fn.icon + ' ' + fn.name + '</span>' +
                    '<span style="font-weight:bold;color:#64b5f6">' + pct + '%</span>' +
                '</div>' +
                '<div style="height:6px;background:#222;border-radius:3px;overflow:hidden">' +
                    '<div style="height:100%;width:' + pct + '%;background:linear-gradient(90deg,#64b5f6,#ba68c8);border-radius:3px"></div>' +
                '</div>' +
            '</div>';
        }).join('');
        document.getElementById('tsFeatures').innerHTML = featsHtml;
    };

    TransportScore.prototype.resize = function () {
        if (this.gaugeChart) this.gaugeChart.resize();
    };

    TransportScore.prototype.dispose = function () {
        if (this._resizeHandler) window.removeEventListener('resize', this._resizeHandler);
        if (this.gaugeChart) { this.gaugeChart.dispose(); this.gaugeChart = null; }
    };

    global.TransportScore = TransportScore;
    if (typeof module !== 'undefined') module.exports = TransportScore;

})(window);
