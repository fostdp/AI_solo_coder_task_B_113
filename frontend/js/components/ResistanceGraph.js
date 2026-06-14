(function (global) {
    'use strict';

    /**
     * 耐药基因传播路径图组件
     * 展示床位节点、GNN预测传播路径、边权重、Fallback状态指示
     */
    function ResistanceGraph(domId, options) {
        this.dom = document.getElementById(domId);
        if (!this.dom) throw new Error('ResistanceGraph: DOM #' + domId + ' not found');
        this.chart = echarts.init(this.dom, 'dark', {
            renderer: 'canvas',
            height: (options && options.height) || 450
        });
        this.nodes = [];
        this.links = [];
        this._initOption();
        var self = this;
        this._resizeHandler = function () { self.resize(); };
        window.addEventListener('resize', this._resizeHandler);
    }

    ResistanceGraph.prototype._initOption = function () {
        this.option = {
            backgroundColor: 'transparent',
            title: {
                text: 'CRE耐药基因传播网络 (GNN+图卷积预测)',
                subtext: '节点大小=风险值 · 边粗细=传播概率 · 虚线=Fallback降级',
                left: 'center',
                textStyle: { color: '#fff', fontSize: 14 },
                subtextStyle: { color: '#aaa', fontSize: 11 }
            },
            tooltip: {
                formatter: function (p) {
                    if (p.dataType === 'node') {
                        return '<b>' + p.data.name + '</b><br/>' +
                            '隔离状态: ' + (p.data.isolation ? '已隔离' : '未隔离') + '<br/>' +
                            '抗生素天数: ' + p.data.abxDays + '天<br/>' +
                            '侵入操作: ' + p.data.invasiveCount + '次<br/>' +
                            '培养结果: ' + (p.data.culturePositive ? '阳性' : '阴性/未知') + '<br/>' +
                            '基线风险: ' + (p.data.baselineRisk * 100).toFixed(0) + '%';
                    }
                    if (p.dataType === 'edge') {
                        var probColor = p.data.prob >= 0.7 ? '#ef5350' : '#ffa726';
                        var fallbackHtml = p.data.isFallback ? '<span style="color:#ff9800">⚠ Fallback预测</span>' : '';
                        return '传播路径: ' + p.data.source + ' → ' + p.data.target + '<br/>' +
                            '概率: <b style="color:' + probColor + '">' + (p.data.prob * 100).toFixed(1) + '%</b><br/>' +
                            '边权重: ' + p.data.weight.toFixed(3) + '<br/>' +
                            fallbackHtml;
                    }
                }
            },
            legend: [{
                data: ['已隔离床位', '未隔离床位', '高概率传播(≥70%)', '中概率传播', 'Fallback降级传播'],
                top: 40,
                textStyle: { color: '#ccc' }
            }],
            animationDurationUpdate: 1500,
            animationEasingUpdate: 'quinticInOut',
            series: [{
                type: 'graph',
                layout: 'force',
                symbolSize: function (val, p) {
                    return 30 + (p.data.baselineRisk || 0.3) * 60;
                },
                roam: true,
                draggable: true,
                force: { repulsion: 400, edgeLength: [80, 200], gravity: 0.1 },
                label: { show: true, position: 'right', color: '#fff', fontSize: 11 },
                edgeSymbol: ['none', 'arrow'],
                edgeSymbolSize: [0, 10],
                emphasis: { focus: 'adjacency', lineStyle: { width: 6 } },
                categories: [
                    { name: '已隔离床位', itemStyle: { color: '#66bb6a' } },
                    { name: '未隔离床位', itemStyle: { color: '#ef5350' } }
                ],
                data: [],
                links: [],
                lineStyle: {
                    opacity: 0.8,
                    curveness: 0.15,
                    width: 3
                }
            }]
        };
        this.chart.setOption(this.option);
    };

    ResistanceGraph.prototype.setData = function (predictions, beds) {
        var bedMap = {};
        (beds || []).forEach(function (b) { bedMap[b.bedId] = b; });

        this.nodes = (beds || []).map(function (b) {
            return {
                id: b.bedId,
                name: b.bedId + '床',
                category: b.isolation ? 0 : 1,
                x: (b.locationX || 0) * 10,
                y: (b.locationY || 0) * 10,
                isolation: !!b.isolation,
                abxDays: b.abxDays || 0,
                invasiveCount: b.invasiveCount || 0,
                culturePositive: !!b.culturePositive,
                baselineRisk: b.baselineRisk || 0.1,
                value: b.baselineRisk || 0.1
            };
        });

        this.links = [];
        var self = this;
        (predictions || []).forEach(function (pred) {
            if (!pred.path || pred.path.length < 2) return;
            for (var i = 0; i < pred.path.length - 1; i++) {
                var src = pred.path[i];
                var tgt = pred.path[i + 1];
                var edgeWeight = pred.edgeWeights && pred.edgeWeights[i] !== undefined
                    ? pred.edgeWeights[i]
                    : (pred.spreadProb * (1 - i * 0.15));
                var prob = Math.max(0, Math.min(1, edgeWeight));
                var lineColor = prob >= 0.7 ? '#ef5350' : (prob >= 0.4 ? '#ffa726' : '#ffd54f');
                self.links.push({
                    source: src + '床',
                    target: tgt + '床',
                    prob: prob,
                    weight: prob,
                    isFallback: !!pred.isFallback,
                    lineStyle: {
                        width: 1 + prob * 8,
                        type: pred.isFallback ? 'dashed' : 'solid',
                        color: lineColor
                    },
                    value: (prob * 100).toFixed(0) + '%'
                });
            }
        });

        this.chart.setOption({
            series: [{ data: this.nodes, links: this.links }]
        });
    };

    ResistanceGraph.prototype.highlightPath = function (bedId) {
        var self = this;
        var highlightLinks = this.links.filter(function (l) {
            return l.source.indexOf(bedId) !== -1 || l.target.indexOf(bedId) !== -1;
        });
        var linkNames = highlightLinks.map(function (l) { return l.source; });
        this.chart.dispatchAction({
            type: 'highlight',
            seriesIndex: 0,
            name: linkNames
        });
    };

    ResistanceGraph.prototype.resize = function () { if (this.chart) this.chart.resize(); };

    ResistanceGraph.prototype.dispose = function () {
        if (this._resizeHandler) window.removeEventListener('resize', this._resizeHandler);
        if (this.chart) { this.chart.dispose(); this.chart = null; }
    };

    global.ResistanceGraph = ResistanceGraph;
    if (typeof module !== 'undefined') module.exports = ResistanceGraph;

})(window);
