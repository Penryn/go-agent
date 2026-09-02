import { computed, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { Search } from '@element-plus/icons-vue';
import { relativeTime } from '@/lib/format';
import { useDashboardStore } from '@/stores/dashboard';
const { snapshot } = storeToRefs(useDashboardStore());
const query = ref('');
const type = ref('');
const types = computed(() => [...new Set(snapshot.value?.memories.map((item) => item.type) || [])]);
const memories = computed(() => (snapshot.value?.memories || []).filter((item) => {
    const matchesType = !type.value || item.type === type.value;
    const needle = query.value.trim().toLowerCase();
    return matchesType && (!needle || `${item.subject} ${item.content} ${item.scope}`.toLowerCase().includes(needle));
}));
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
(__VLS_ctx.memories.length);
__VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
    ...{ class: "filters" },
});
/** @type {__VLS_StyleScopedClasses['filters']} */ ;
let __VLS_0;
/** @ts-ignore @type { | typeof __VLS_components.elInput | typeof __VLS_components.ElInput | typeof __VLS_components['el-input']} */
elInput;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent1(__VLS_0, new __VLS_0({
    modelValue: (__VLS_ctx.query),
    prefixIcon: (__VLS_ctx.Search),
    clearable: true,
    placeholder: "搜索内容或作用域",
}));
const __VLS_2 = __VLS_1({
    modelValue: (__VLS_ctx.query),
    prefixIcon: (__VLS_ctx.Search),
    clearable: true,
    placeholder: "搜索内容或作用域",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_5;
/** @ts-ignore @type { | typeof __VLS_components.elSelect | typeof __VLS_components.ElSelect | typeof __VLS_components['el-select'] | typeof __VLS_components.elSelect | typeof __VLS_components.ElSelect | typeof __VLS_components['el-select']} */
elSelect;
// @ts-ignore
const __VLS_6 = __VLS_asFunctionalComponent1(__VLS_5, new __VLS_5({
    modelValue: (__VLS_ctx.type),
    clearable: true,
    placeholder: "全部类型",
}));
const __VLS_7 = __VLS_6({
    modelValue: (__VLS_ctx.type),
    clearable: true,
    placeholder: "全部类型",
}, ...__VLS_functionalComponentArgsRest(__VLS_6));
const { default: __VLS_10 } = __VLS_8.slots;
for (const [item] of __VLS_vFor((__VLS_ctx.types))) {
    let __VLS_11;
    /** @ts-ignore @type { | typeof __VLS_components.elOption | typeof __VLS_components.ElOption | typeof __VLS_components['el-option']} */
    elOption;
    // @ts-ignore
    const __VLS_12 = __VLS_asFunctionalComponent1(__VLS_11, new __VLS_11({
        key: (item),
        label: (item),
        value: (item),
    }));
    const __VLS_13 = __VLS_12({
        key: (item),
        label: (item),
        value: (item),
    }, ...__VLS_functionalComponentArgsRest(__VLS_12));
    // @ts-ignore
    [memories, query, Search, type, types,];
}
// @ts-ignore
[];
var __VLS_8;
if (__VLS_ctx.memories.length) {
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "memory-grid" },
    });
    /** @type {__VLS_StyleScopedClasses['memory-grid']} */ ;
    for (const [item] of __VLS_vFor((__VLS_ctx.memories))) {
        __VLS_asFunctionalElement1(__VLS_intrinsics.article, __VLS_intrinsics.article)({
            key: (item.id),
            ...{ class: "memory-card" },
        });
        /** @type {__VLS_StyleScopedClasses['memory-card']} */ ;
        __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
            ...{ class: "memory-meta" },
        });
        /** @type {__VLS_StyleScopedClasses['memory-meta']} */ ;
        let __VLS_16;
        /** @ts-ignore @type { | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag'] | typeof __VLS_components.elTag | typeof __VLS_components.ElTag | typeof __VLS_components['el-tag']} */
        elTag;
        // @ts-ignore
        const __VLS_17 = __VLS_asFunctionalComponent1(__VLS_16, new __VLS_16({
            effect: "plain",
            round: true,
        }));
        const __VLS_18 = __VLS_17({
            effect: "plain",
            round: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_17));
        const { default: __VLS_21 } = __VLS_19.slots;
        (item.type);
        // @ts-ignore
        [memories, memories,];
        var __VLS_19;
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (__VLS_ctx.relativeTime(item.created_at));
        __VLS_asFunctionalElement1(__VLS_intrinsics.h3, __VLS_intrinsics.h3)({});
        (item.subject || item.scope);
        __VLS_asFunctionalElement1(__VLS_intrinsics.p, __VLS_intrinsics.p)({});
        (item.content);
        __VLS_asFunctionalElement1(__VLS_intrinsics.footer, __VLS_intrinsics.footer)({});
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.scope);
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.importance.toFixed(2));
        __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
        (item.confidence.toFixed(2));
        // @ts-ignore
        [relativeTime,];
    }
}
else {
    let __VLS_22;
    /** @ts-ignore @type { | typeof __VLS_components.elEmpty | typeof __VLS_components.ElEmpty | typeof __VLS_components['el-empty']} */
    elEmpty;
    // @ts-ignore
    const __VLS_23 = __VLS_asFunctionalComponent1(__VLS_22, new __VLS_22({
        description: "没有符合条件的记忆",
    }));
    const __VLS_24 = __VLS_23({
        description: "没有符合条件的记忆",
    }, ...__VLS_functionalComponentArgsRest(__VLS_23));
}
// @ts-ignore
[];
const __VLS_export = (await import('vue')).defineComponent({});
export default {};
