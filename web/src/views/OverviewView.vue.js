import { computed } from 'vue';
import { storeToRefs } from 'pinia';
import { useDashboardStore } from '@/stores/dashboard';
import { activityText, energyText, moodText, relativeTime } from '@/lib/format';
const store = useDashboardStore();
const { snapshot, loading } = storeToRefs(store);
const persona = computed(() => snapshot.value?.persona);
const stats = computed(() => [
    { label: '活跃群聊', value: snapshot.value?.stats.groups ?? 0, code: 'GROUPS', tone: 'mint' },
    { label: '已识别群友', value: snapshot.value?.stats.members ?? 0, code: 'PEOPLE', tone: 'violet' },
    { label: '有效记忆', value: snapshot.value?.stats.memories ?? 0, code: 'MEMORY', tone: 'amber' },
    { label: '后台任务', value: snapshot.value?.stats.pending_tasks ?? 0, code: 'QUEUE', tone: 'blue' },
]);
const __VLS_ctx = {
    ...{},
    ...{},
};
let __VLS_components;
let __VLS_intrinsics;
let __VLS_directives;
let __VLS_0;
/** @ts-ignore @type { | typeof __VLS_components.elSkeleton | typeof __VLS_components.ElSkeleton | typeof __VLS_components['el-skeleton'] | typeof __VLS_components.elSkeleton | typeof __VLS_components.ElSkeleton | typeof __VLS_components['el-skeleton']} */
elSkeleton;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent1(__VLS_0, new __VLS_0({
    loading: (__VLS_ctx.loading && !__VLS_ctx.snapshot),
    animated: true,
    rows: (8),
}));
const __VLS_2 = __VLS_1({
    loading: (__VLS_ctx.loading && !__VLS_ctx.snapshot),
    animated: true,
    rows: (8),
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
var __VLS_5;
const { default: __VLS_6 } = __VLS_3.slots;
__VLS_asFunctionalElement1(__VLS_intrinsics.section, __VLS_intrinsics.section)({
    ...{ class: "metric-grid" },
});
/** @type {__VLS_StyleScopedClasses['metric-grid']} */ ;
for (const [item] of __VLS_vFor((__VLS_ctx.stats))) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
        key: (item.label),
        ...{ class: "metric-card" },
        'data-tone': (item.tone),
    });
    /** @type {__VLS_StyleScopedClasses['metric-card']} */ ;
    __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
    (item.label);
    __VLS_asFunctionalElement1(__VLS_intrinsics.small, __VLS_intrinsics.small)({});
    (item.code);
    __VLS_asFunctionalElement1(__VLS_intrinsics.strong, __VLS_intrinsics.strong)({});
    (item.value);
    __VLS_asFunctionalElement1(__VLS_intrinsics.i)({});
    // @ts-ignore
    [loading, snapshot, stats,];
}
__VLS_asFunctionalElement1(__VLS_intrinsics.section, __VLS_intrinsics.section)({
    ...{ class: "overview-grid" },
});
/** @type {__VLS_StyleScopedClasses['overview-grid']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
    ...{ class: "glass-panel identity-panel" },
});
/** @type {__VLS_StyleScopedClasses['glass-panel']} */ ;
/** @type {__VLS_StyleScopedClasses['identity-panel']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "panel-title" },
});
/** @type {__VLS_StyleScopedClasses['panel-title']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.h2, __VLS_intrinsics.h2)({});
let __VLS_7;
/** @ts-ignore @type { | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag'] | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag']} */
elTag;
// @ts-ignore
const __VLS_8 = __VLS_asFunctionalComponent1(__VLS_7, new __VLS_7({
    effect: "dark",
    round: true,
}));
const __VLS_9 = __VLS_8({
    effect: "dark",
    round: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_8));
const { default: __VLS_12 } = __VLS_10.slots;
(__VLS_ctx.snapshot?.status.qq_enabled ? '在线' : '离线');
// @ts-ignore
[snapshot,];
var __VLS_10;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "identity-hero" },
});
/** @type {__VLS_StyleScopedClasses['identity-hero']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "portrait" },
});
/** @type {__VLS_StyleScopedClasses['portrait']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
(__VLS_ctx.persona?.name?.slice(0, 1) || '芙');
__VLS_asFunctionalElement1(__VLS_intrinsics.i)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "identity-copy" },
});
/** @type {__VLS_StyleScopedClasses['identity-copy']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.h3, __VLS_intrinsics.h3)({});
(__VLS_ctx.persona?.name || 'Bot');
__VLS_asFunctionalElement1(__VLS_intrinsics.p, __VLS_intrinsics.p)({});
(__VLS_ctx.persona?.description || '等待身份数据');
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "state-pills" },
});
/** @type {__VLS_StyleScopedClasses['state-pills']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.b, __VLS_intrinsics.b)({});
(__VLS_ctx.moodText(__VLS_ctx.persona?.mood));
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.b, __VLS_intrinsics.b)({});
(__VLS_ctx.energyText(__VLS_ctx.persona?.energy));
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.b, __VLS_intrinsics.b)({});
((__VLS_ctx.persona?.talk_bias ?? 0).toFixed(2));
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.b, __VLS_intrinsics.b)({});
(__VLS_ctx.persona?.runtime.state || 'observing');
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "fact-grid" },
});
/** @type {__VLS_StyleScopedClasses['fact-grid']} */ ;
for (const [fact] of __VLS_vFor((__VLS_ctx.persona?.facts || []))) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        key: (fact.fact_id),
        ...{ class: "fact-cell" },
    });
    /** @type {__VLS_StyleScopedClasses['fact-cell']} */ ;
    __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
    (fact.key);
    __VLS_asFunctionalElement1(__VLS_intrinsics.strong, __VLS_intrinsics.strong)({
        title: (fact.value),
    });
    (fact.value);
    // @ts-ignore
    [persona, persona, persona, persona, persona, persona, persona, persona, moodText, energyText,];
}
if (!__VLS_ctx.persona?.facts.length) {
    let __VLS_13;
    /** @ts-ignore @type { | typeof __VLS_components.elEmpty | typeof __VLS_components.ElEmpty | typeof __VLS_components['el-empty']} */
    elEmpty;
    // @ts-ignore
    const __VLS_14 = __VLS_asFunctionalComponent1(__VLS_13, new __VLS_13({
        description: "暂无人物事实",
        imageSize: (56),
    }));
    const __VLS_15 = __VLS_14({
        description: "暂无人物事实",
        imageSize: (56),
    }, ...__VLS_functionalComponentArgsRest(__VLS_14));
}
__VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
    ...{ class: "glass-panel relation-panel" },
});
/** @type {__VLS_StyleScopedClasses['glass-panel']} */ ;
/** @type {__VLS_StyleScopedClasses['relation-panel']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "panel-title" },
});
/** @type {__VLS_StyleScopedClasses['panel-title']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.h2, __VLS_intrinsics.h2)({});
let __VLS_18;
/** @ts-ignore @type { | typeof __VLS_components.RouterLink | typeof __VLS_components.RouterLink} */
RouterLink;
// @ts-ignore
const __VLS_19 = __VLS_asFunctionalComponent1(__VLS_18, new __VLS_18({
    to: "/relations",
}));
const __VLS_20 = __VLS_19({
    to: "/relations",
}, ...__VLS_functionalComponentArgsRest(__VLS_19));
const { default: __VLS_23 } = __VLS_21.slots;
// @ts-ignore
[persona,];
var __VLS_21;
if (__VLS_ctx.snapshot?.relationships.length) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "relation-list" },
    });
    /** @type {__VLS_StyleScopedClasses['relation-list']} */ ;
    for (const [item, index] of __VLS_vFor((__VLS_ctx.snapshot.relationships.slice(0, 6)))) {
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            key: (item.user_id),
            ...{ class: "relation-row" },
        });
        /** @type {__VLS_StyleScopedClasses['relation-row']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "rank" },
        });
        /** @type {__VLS_StyleScopedClasses['rank']} */ ;
        (String(index + 1).padStart(2, '0'));
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "member" },
        });
        /** @type {__VLS_StyleScopedClasses['member']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.strong, __VLS_intrinsics.strong)({});
        (item.name);
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.message_count);
        (__VLS_ctx.relativeTime(item.last_interact_at));
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "affinity" },
        });
        /** @type {__VLS_StyleScopedClasses['affinity']} */ ;
        let __VLS_24;
        /** @ts-ignore @type { | typeof __VLS_components.elProgress | typeof __VLS_components.ElProgress | typeof __VLS_components['el-progress']} */
        elProgress;
        // @ts-ignore
        const __VLS_25 = __VLS_asFunctionalComponent1(__VLS_24, new __VLS_24({
            percentage: (Math.round(item.affinity * 100)),
            showText: (false),
            strokeWidth: (5),
        }));
        const __VLS_26 = __VLS_25({
            percentage: (Math.round(item.affinity * 100)),
            showText: (false),
            strokeWidth: (5),
        }, ...__VLS_functionalComponentArgsRest(__VLS_25));
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (Math.round(item.affinity * 100));
        // @ts-ignore
        [snapshot, snapshot, relativeTime,];
    }
}
else {
    let __VLS_29;
    /** @ts-ignore @type { | typeof __VLS_components.elEmpty | typeof __VLS_components.ElEmpty | typeof __VLS_components['el-empty']} */
    elEmpty;
    // @ts-ignore
    const __VLS_30 = __VLS_asFunctionalComponent1(__VLS_29, new __VLS_29({
        description: "还没有形成群友关系",
        imageSize: (70),
    }));
    const __VLS_31 = __VLS_30({
        description: "还没有形成群友关系",
        imageSize: (70),
    }, ...__VLS_functionalComponentArgsRest(__VLS_30));
}
__VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
    ...{ class: "glass-panel activity-panel" },
});
/** @type {__VLS_StyleScopedClasses['glass-panel']} */ ;
/** @type {__VLS_StyleScopedClasses['activity-panel']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "panel-title" },
});
/** @type {__VLS_StyleScopedClasses['panel-title']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.h2, __VLS_intrinsics.h2)({});
let __VLS_34;
/** @ts-ignore @type { | typeof __VLS_components.RouterLink | typeof __VLS_components.RouterLink} */
RouterLink;
// @ts-ignore
const __VLS_35 = __VLS_asFunctionalComponent1(__VLS_34, new __VLS_34({
    to: "/activity",
}));
const __VLS_36 = __VLS_35({
    to: "/activity",
}, ...__VLS_functionalComponentArgsRest(__VLS_35));
const { default: __VLS_39 } = __VLS_37.slots;
// @ts-ignore
[];
var __VLS_37;
if (__VLS_ctx.snapshot?.activity.length) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "activity-stream" },
    });
    /** @type {__VLS_StyleScopedClasses['activity-stream']} */ ;
    for (const [item] of __VLS_vFor((__VLS_ctx.snapshot.activity.slice(0, 8)))) {
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            key: (`${item.type}-${item.at}-${item.subject}`),
            ...{ class: "activity-row" },
        });
        /** @type {__VLS_StyleScopedClasses['activity-row']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.i)({
            'data-type': (item.type),
        });
        __VLS_asFunctionalElement1(__VLS_intrinsics.time, __VLS_intrinsics.time)({});
        (__VLS_ctx.relativeTime(item.at));
        let __VLS_40;
        /** @ts-ignore @type { | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag'] | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag']} */
        elTag;
        // @ts-ignore
        const __VLS_41 = __VLS_asFunctionalComponent1(__VLS_40, new __VLS_40({
            size: "small",
            effect: "plain",
        }));
        const __VLS_42 = __VLS_41({
            size: "small",
            effect: "plain",
        }, ...__VLS_functionalComponentArgsRest(__VLS_41));
        const { default: __VLS_45 } = __VLS_43.slots;
        (__VLS_ctx.activityText(item.type));
        // @ts-ignore
        [snapshot, snapshot, relativeTime, activityText,];
        var __VLS_43;
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
        __VLS_asFunctionalElement1(__VLS_intrinsics.strong, __VLS_intrinsics.strong)({});
        (item.subject || item.label);
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.detail || '—');
        // @ts-ignore
        [];
    }
}
else {
    let __VLS_46;
    /** @ts-ignore @type { | typeof __VLS_components.elEmpty | typeof __VLS_components.ElEmpty | typeof __VLS_components['el-empty']} */
    elEmpty;
    // @ts-ignore
    const __VLS_47 = __VLS_asFunctionalComponent1(__VLS_46, new __VLS_46({
        description: "暂无运行记录",
        imageSize: (70),
    }));
    const __VLS_48 = __VLS_47({
        description: "暂无运行记录",
        imageSize: (70),
    }, ...__VLS_functionalComponentArgsRest(__VLS_47));
}
// @ts-ignore
[];
var __VLS_3;
// @ts-ignore
[];
const __VLS_export = (await import('vue')).defineComponent({});
export default {};
