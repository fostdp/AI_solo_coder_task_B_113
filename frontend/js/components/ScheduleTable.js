(function (global) {
    'use strict';

    /**
     * 调度排班表组件
     * 展示NP/病房分配、护士排班、L1正则化变化率、贪心解标记、排队建议
     */
    function ScheduleTable(containerId, options) {
        this.container = document.getElementById(containerId);
        if (!this.container) throw new Error('ScheduleTable: DOM #' + containerId + ' not found');
        this.options = options || {};
        this.data = null;
        this._renderSkeleton();
    }

    ScheduleTable.prototype._renderSkeleton = function () {
        var self = this;
        this.container.innerHTML =
            '<div class="schedule-header" style="margin-bottom:16px">' +
                '<h3 style="color:#fff;margin:0 0 8px 0">' +
                    '<i class="fas fa-calendar-check"></i> 移动ICU智能资源调度' +
                '</h3>' +
                '<div id="schMeta" class="sch-meta" style="display:flex;gap:20px;flex-wrap:wrap;color:#aaa;font-size:13px"></div>' +
            '</div>' +
            '<div id="schWarnings" class="sch-warnings" style="margin-bottom:12px"></div>' +
            '<div class="sch-tabs" style="display:flex;gap:8px;margin-bottom:12px">' +
                '<button class="sch-tab active" data-tab="rooms" style="padding:8px 16px;background:#2a3752;color:#fff;border:1px solid #3f5175;border-radius:4px;cursor:pointer">🏥 房间分配</button>' +
                '<button class="sch-tab" data-tab="nurses" style="padding:8px 16px;background:#1e2a3e;color:#aaa;border:1px solid #3f5175;border-radius:4px;cursor:pointer">👩‍⚕️ 护士排班</button>' +
                '<button class="sch-tab" data-tab="suggestions" style="padding:8px 16px;background:#1e2a3e;color:#aaa;border:1px solid #3f5175;border-radius:4px;cursor:pointer">💡 调度建议</button>' +
            '</div>' +
            '<div id="schRooms" class="sch-panel active"></div>' +
            '<div id="schNurses" class="sch-panel" style="display:none"></div>' +
            '<div id="schSuggestions" class="sch-panel" style="display:none"></div>';

        var tabs = this.container.querySelectorAll('.sch-tab');
        tabs.forEach(function (btn) {
            btn.addEventListener('click', function () {
                self._switchTab(btn.dataset.tab);
            });
        });
    };

    ScheduleTable.prototype._switchTab = function (tab) {
        var tabs = this.container.querySelectorAll('.sch-tab');
        var self = this;
        tabs.forEach(function (b) {
            var isActive = b.dataset.tab === tab;
            b.classList.toggle('active', isActive);
            b.style.background = isActive ? '#2a3752' : '#1e2a3e';
            b.style.color = isActive ? '#fff' : '#aaa';
        });
        ['rooms', 'nurses', 'suggestions'].forEach(function (t) {
            var panelId = 'sch' + t.charAt(0).toUpperCase() + t.slice(1);
            var el = self.container.querySelector('#' + panelId);
            if (el) el.style.display = t === tab ? 'block' : 'none';
        });
    };

    ScheduleTable.prototype.setData = function (solution) {
        this.data = solution || {};
        this._renderMeta();
        this._renderWarnings();
        this._renderRooms();
        this._renderNurses();
        this._renderSuggestions();
    };

    ScheduleTable.prototype._renderMeta = function () {
        var s = this.data || {};
        var changeRate = s.changeRate || 0;
        var changeRateColor = changeRate < 0.1 ? '#66bb6a' : (changeRate < 0.3 ? '#ffa726' : '#ef5350');
        var statusHtml = s.isGreedyFallback
            ? '<span style="color:#ff9800">⚠ 贪心降级解</span>'
            : '<span style="color:#66bb6a">✓ 最优解</span>';
        var totalCost = (s.objective && s.objective.TotalCost !== undefined) ? s.objective.TotalCost : 0;

        document.getElementById('schMeta').innerHTML =
            '<span>📋 方案ID: <code style="background:#2a3752;padding:2px 6px;border-radius:3px">' + (s.solutionId || '-') + '</code></span>' +
            '<span>⏱️ 求解耗时: <b>' + (s.solveTimeMs || 0) + 'ms</b></span>' +
            '<span>📊 L1变化率: <b style="color:' + changeRateColor + '">' + (changeRate * 100).toFixed(1) + '%</b></span>' +
            '<span>🎯 状态: ' + statusHtml + '</span>' +
            '<span>📈 总成本: <b>' + totalCost.toFixed(3) + '</b></span>';
    };

    ScheduleTable.prototype._renderWarnings = function () {
        var s = this.data || {};
        var unmet = s.unmetNeeds || [];
        var html = unmet.map(function (u) {
            return '<div style="padding:10px 16px;background:rgba(255,152,0,0.1);border-left:4px solid #ff9800;border-radius:4px;margin-bottom:6px;color:#ffcc80">' +
                '<b>📦 排队建议:</b> ' + u +
            '</div>';
        }).join('');
        document.getElementById('schWarnings').innerHTML = html || '';
    };

    ScheduleTable.prototype._renderRooms = function () {
        var s = this.data || {};
        var assigns = s.assignments || {};
        var entries = Object.entries(assigns);
        entries.sort(function (a, b) { return Number(a[0]) - Number(b[0]); });

        var npCount = 0;
        Object.values(assigns).forEach(function (v) {
            if (typeof v === 'string' && v.indexOf('NP') === 0) npCount++;
        });

        var cardsHtml = entries.map(function (entry) {
            var bedId = entry[0];
            var room = entry[1];
            var isNP = typeof room === 'string' && room.indexOf('NP') === 0;
            var bgStyle = isNP ? 'rgba(100,181,246,0.08)' : 'rgba(129,199,132,0.05)';
            var badgeBg = isNP ? '#1976d2' : '#2e7d32';
            return '<div style="padding:12px;border-radius:8px;border:1px solid #3f5175;background:' + bgStyle + '">' +
                '<div style="display:flex;justify-content:space-between;align-items:center">' +
                    '<span style="font-size:16px;font-weight:bold;color:#fff">🏥 ' + bedId + '床</span>' +
                    '<span style="padding:4px 10px;border-radius:12px;font-size:11px;font-weight:bold;background:' + badgeBg + ';color:#fff">' + room + '</span>' +
                '</div>' +
            '</div>';
        }).join('');

        var summaryHtml =
            '<div style="display:flex;justify-content:space-between;margin-bottom:12px;color:#ccc">' +
                '<span>🏥 床位分配总数: <b style="color:#fff">' + entries.length + '</b></span>' +
                '<span>🟦 负压病房: <b style="color:#64b5f6">' + npCount + '</b></span>' +
                '<span>🟩 普通病房: <b style="color:#81c784">' + (entries.length - npCount) + '</b></span>' +
            '</div>';

        var gridHtml = '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:10px">' + cardsHtml + '</div>';

        document.getElementById('schRooms').innerHTML = summaryHtml + gridHtml;
    };

    ScheduleTable.prototype._renderNurses = function () {
        var s = this.data || {};
        var schedule = s.schedule || {};
        var entries = Object.entries(schedule);
        entries.sort();

        var cardsHtml = entries.map(function (entry) {
            var nurse = entry[0];
            var beds = entry[1] || [];
            var sortedBeds = beds.slice().sort(function (a, b) { return Number(a) - Number(b); });
            var bedTags = sortedBeds.map(function (bed) {
                return '<span style="padding:4px 10px;background:#2a3752;border-radius:6px;font-size:12px;color:#fff">' + bed + '床</span>';
            }).join('');

            return '<div style="padding:14px;border-radius:8px;border:1px solid #3f5175;background:rgba(255,193,7,0.05)">' +
                '<div style="display:flex;justify-content:space-between;margin-bottom:10px">' +
                    '<span style="font-weight:bold;color:#fff">👩‍⚕️ ' + nurse + '</span>' +
                    '<span style="padding:3px 10px;background:#ff9800;border-radius:12px;font-size:11px;color:#fff;font-weight:bold">' + beds.length + '床</span>' +
                '</div>' +
                '<div style="display:flex;flex-wrap:wrap;gap:6px">' + bedTags + '</div>' +
            '</div>';
        }).join('');

        document.getElementById('schNurses').innerHTML =
            '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:12px">' + cardsHtml + '</div>';
    };

    ScheduleTable.prototype._renderSuggestions = function () {
        var s = this.data || {};
        var sugs = s.suggestions || [];

        if (sugs.length === 0) {
            document.getElementById('schSuggestions').innerHTML =
                '<div style="padding:20px;text-align:center;color:#888">✓ 当前方案无需调整建议</div>';
            return;
        }

        var typeMap = {
            room_swap: { icon: '🏥', color: '#64b5f6', label: '房间调整' },
            nurse_reassign: { icon: '👩‍⚕️', color: '#ffb74d', label: '护士调整' },
            queue_priority: { icon: '📋', color: '#ef5350', label: '排队建议' }
        };

        var itemsHtml = sugs.map(function (sug) {
            var meta = typeMap[sug.type] || { icon: '📌', color: '#aaa', label: sug.type || '其他' };
            var fromTo = '';
            if (sug.from && sug.to) {
                fromTo = '<span style="color:#aaa">' + sug.from + ' → <b style="color:#fff">' + sug.to + '</b></span>';
            }
            var reason = sug.reason || '';
            return '<div style="padding:14px;border-radius:8px;border-left:4px solid ' + meta.color + ';background:rgba(255,255,255,0.03);display:flex;gap:14px;align-items:flex-start">' +
                '<div style="font-size:24px">' + meta.icon + '</div>' +
                '<div style="flex:1">' +
                    '<div style="display:flex;gap:10px;align-items:center;margin-bottom:6px;flex-wrap:wrap">' +
                        '<span style="padding:3px 10px;background:' + meta.color + '22;color:' + meta.color + ';border-radius:12px;font-size:11px;font-weight:bold">' + meta.label + '</span>' +
                        (sug.bedId ? '<b style="color:#fff">' + sug.bedId + '床</b>' : '') +
                        fromTo +
                    '</div>' +
                    '<div style="color:#ccc;font-size:13px">' + reason + '</div>' +
                '</div>' +
            '</div>';
        }).join('');

        document.getElementById('schSuggestions').innerHTML =
            '<div style="display:flex;flex-direction:column;gap:10px">' + itemsHtml + '</div>';
    };

    global.ScheduleTable = ScheduleTable;
    if (typeof module !== 'undefined') module.exports = ScheduleTable;

})(window);
