import { computed, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { activityText, relativeTime } from '@/lib/format';
import { useDashboardStore } from '@/stores/dashboard';
const { snapshot } = storeToRefs(useDashboardStore());
const filter = ref('all');
const rows = computed(() => (snapshot.value?.activity || []).filter((item) => filter.value === 'all' || item.type === filter.value));
const __VLS_ctx = {
    ...{},
    ...{},
};
let __VLS_components;
let __VLS_intrinsics;
let __VLS_directives;
__VLS_asFunctionalElement1(__VLS_intrinsics.section, __VLS_intrinsics.section)({
    ...{ class: "glass-panel page-panel" },
});
/** @type {__VLS_StyleScopedClasses['glass-panel']} */ ;
/** @type {__VLS_StyleScopedClasses['page-panel']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "page-panel-head" },
});
/** @type {__VLS_StyleScopedClasses['page-panel-head']} */ ;
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.h2, __VLS_intrinsics.h2)({});
__VLS_asFunctionalElement1(__VLS_intrinsics.p, __VLS_intrinsics.p)({});
let __VLS_0;
/** @ts-ignore @type { | typeof __VLS_components.elSegmented | typeof __VLS_components.ElSegmented | typeof __VLS_components['el-segmented']} */
elSegmented;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent1(__VLS_0, new __VLS_0({
    modelValue: (__VLS_ctx.filter),
    options: ([{ label: '全部', value: 'all' }, { label: '消息', value: 'message' }, { label: '决策', value: 'decision' }, { label: '任务', value: 'task' }]),
}));
const __VLS_2 = __VLS_1({
    modelValue: (__VLS_ctx.filter),
    options: ([{ label: '全部', value: 'all' }, { label: '消息', value: 'message' }, { label: '决策', value: 'decision' }, { label: '任务', value: 'task' }]),
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
if (__VLS_ctx.rows.length) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "timeline" },
    });
    /** @type {__VLS_StyleScopedClasses['timeline']} */ ;
    for (const [item] of __VLS_vFor((__VLS_ctx.rows))) {
        __VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
            key: (`${item.type}-${item.at}-${item.subject}`),
            ...{ class: "timeline-item" },
        });
        /** @type {__VLS_StyleScopedClasses['timeline-item']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "timeline-rail" },
        });
        /** @type {__VLS_StyleScopedClasses['timeline-rail']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.i)({
            'data-type': (item.type),
        });
        __VLS_asFunctionalElement1(__VLS_intrinsics.time, __VLS_intrinsics.time)({});
        (__VLS_ctx.relativeTime(item.at));
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "timeline-copy" },
        });
        /** @type {__VLS_StyleScopedClasses['timeline-copy']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
        let __VLS_5;
        /** @ts-ignore @type { | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag'] | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag']} */
        elTag;
        // @ts-ignore
        const __VLS_6 = __VLS_asFunctionalComponent1(__VLS_5, new __VLS_5({
            effect: "plain",
            round: true,
        }));
        const __VLS_7 = __VLS_6({
            effect: "plain",
            round: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_6));
        const { default: __VLS_10 } = __VLS_8.slots;
        (__VLS_ctx.activityText(item.type));
        // @ts-ignore
        [filter, rows, rows, relativeTime, activityText,];
        var __VLS_8;
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.label);
        if (item.group_id) {
            (item.group_id);
        }
        __VLS_asFunctionalElement1(__VLS_intrinsics.h3, __VLS_intrinsics.h3)({});
        (item.subject || item.label);
        __VLS_asFunctionalElement1(__VLS_intrinsics.p, __VLS_intrinsics.p)({});
        (item.detail || '—');
        // @ts-ignore
        [];
    }
}
else {
    let __VLS_11;
    /** @ts-ignore @type { | typeof __VLS_components.elEmpty | typeof __VLS_components.ElEmpty | typeof __VLS_components['el-empty']} */
    elEmpty;
    // @ts-ignore
    const __VLS_12 = __VLS_asFunctionalComponent1(__VLS_11, new __VLS_11({
        description: "暂无运行记录",
    }));
    const __VLS_13 = __VLS_12({
        description: "暂无运行记录",
    }, ...__VLS_functionalComponentArgsRest(__VLS_12));
}
// @ts-ignore
[];
const __VLS_export = (await import('vue')).defineComponent({});
export default {};
