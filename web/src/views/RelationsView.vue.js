import { computed, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { Search } from '@element-plus/icons-vue';
import { relativeTime } from '@/lib/format';
import { useDashboardStore } from '@/stores/dashboard';
const { snapshot } = storeToRefs(useDashboardStore());
const query = ref('');
const rows = computed(() => (snapshot.value?.relationships || []).filter((item) => `${item.name} ${item.user_id}`.toLowerCase().includes(query.value.trim().toLowerCase())));
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
/** @ts-ignore @type { | typeof __VLS_components.elInput | typeof __VLS_components.ElInput | typeof __VLS_components['el-input']} */
elInput;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent1(__VLS_0, new __VLS_0({
    modelValue: (__VLS_ctx.query),
    prefixIcon: (__VLS_ctx.Search),
    clearable: true,
    ...{ class: "search-box" },
    placeholder: "搜索昵称或 QQ",
}));
const __VLS_2 = __VLS_1({
    modelValue: (__VLS_ctx.query),
    prefixIcon: (__VLS_ctx.Search),
    clearable: true,
    ...{ class: "search-box" },
    placeholder: "搜索昵称或 QQ",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
/** @type {__VLS_StyleScopedClasses['search-box']} */ ;
let __VLS_5;
/** @ts-ignore @type { | typeof __VLS_components.elTable | typeof __VLS_components.ElTable | typeof __VLS_components['el-table'] | typeof __VLS_components.elTable | typeof __VLS_components.ElTable | typeof __VLS_components['el-table']} */
elTable;
// @ts-ignore
const __VLS_6 = __VLS_asFunctionalComponent1(__VLS_5, new __VLS_5({
    data: (__VLS_ctx.rows),
    ...{ class: "relation-table" },
    emptyText: "还没有关系数据",
}));
const __VLS_7 = __VLS_6({
    data: (__VLS_ctx.rows),
    ...{ class: "relation-table" },
    emptyText: "还没有关系数据",
}, ...__VLS_functionalComponentArgsRest(__VLS_6));
/** @type {__VLS_StyleScopedClasses['relation-table']} */ ;
const { default: __VLS_10 } = __VLS_8.slots;
let __VLS_11;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_12 = __VLS_asFunctionalComponent1(__VLS_11, new __VLS_11({
    label: "群友",
    minWidth: "190",
}));
const __VLS_13 = __VLS_12({
    label: "群友",
    minWidth: "190",
}, ...__VLS_functionalComponentArgsRest(__VLS_12));
const { default: __VLS_16 } = __VLS_14.slots;
{
    const { default: __VLS_17 } = __VLS_14.slots;
    const [{ row }] = __VLS_vSlot(__VLS_17);
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "table-member" },
    });
    /** @type {__VLS_StyleScopedClasses['table-member']} */ ;
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({});
    (row.name.slice(0, 1));
    __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
    __VLS_asFunctionalElement1(__VLS_intrinsics.strong, __VLS_intrinsics.strong)({});
    (row.name);
    __VLS_asFunctionalElement1(__VLS_intrinsics.small, __VLS_intrinsics.small)({});
    (row.user_id);
    // @ts-ignore
    [query, Search, rows,];
}
// @ts-ignore
[];
var __VLS_14;
let __VLS_18;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_19 = __VLS_asFunctionalComponent1(__VLS_18, new __VLS_18({
    label: "好感度",
    minWidth: "190",
    sortable: true,
    prop: "affinity",
}));
const __VLS_20 = __VLS_19({
    label: "好感度",
    minWidth: "190",
    sortable: true,
    prop: "affinity",
}, ...__VLS_functionalComponentArgsRest(__VLS_19));
const { default: __VLS_23 } = __VLS_21.slots;
{
    const { default: __VLS_24 } = __VLS_21.slots;
    const [{ row }] = __VLS_vSlot(__VLS_24);
    __VLS_asFunctionalElement1(__VLS_intrinsics.div, __VLS_intrinsics.div)({
        ...{ class: "table-progress" },
    });
    /** @type {__VLS_StyleScopedClasses['table-progress']} */ ;
    let __VLS_25;
    /** @ts-ignore @type { | typeof __VLS_components.elProgress | typeof __VLS_components.ElProgress | typeof __VLS_components['el-progress']} */
    elProgress;
    // @ts-ignore
    const __VLS_26 = __VLS_asFunctionalComponent1(__VLS_25, new __VLS_25({
        percentage: (Math.round(row.affinity * 100)),
        strokeWidth: (6),
    }));
    const __VLS_27 = __VLS_26({
        percentage: (Math.round(row.affinity * 100)),
        strokeWidth: (6),
    }, ...__VLS_functionalComponentArgsRest(__VLS_26));
    __VLS_asFunctionalElement1(__VLS_intrinsics.span, __VLS_intrinsics.span)({});
    (row.affinity.toFixed(2));
    // @ts-ignore
    [];
}
// @ts-ignore
[];
var __VLS_21;
let __VLS_30;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_31 = __VLS_asFunctionalComponent1(__VLS_30, new __VLS_30({
    label: "熟悉度",
    width: "110",
    sortable: true,
    prop: "familiarity",
}));
const __VLS_32 = __VLS_31({
    label: "熟悉度",
    width: "110",
    sortable: true,
    prop: "familiarity",
}, ...__VLS_functionalComponentArgsRest(__VLS_31));
const { default: __VLS_35 } = __VLS_33.slots;
{
    const { default: __VLS_36 } = __VLS_33.slots;
    const [{ row }] = __VLS_vSlot(__VLS_36);
    (row.familiarity.toFixed(2));
    // @ts-ignore
    [];
}
// @ts-ignore
[];
var __VLS_33;
let __VLS_37;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_38 = __VLS_asFunctionalComponent1(__VLS_37, new __VLS_37({
    label: "玩笑容忍",
    width: "110",
    prop: "tease_tolerance",
}));
const __VLS_39 = __VLS_38({
    label: "玩笑容忍",
    width: "110",
    prop: "tease_tolerance",
}, ...__VLS_functionalComponentArgsRest(__VLS_38));
const { default: __VLS_42 } = __VLS_40.slots;
{
    const { default: __VLS_43 } = __VLS_40.slots;
    const [{ row }] = __VLS_vSlot(__VLS_43);
    (row.tease_tolerance.toFixed(2));
    // @ts-ignore
    [];
}
// @ts-ignore
[];
var __VLS_40;
let __VLS_44;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent1(__VLS_44, new __VLS_44({
    label: "互动",
    width: "100",
    sortable: true,
    prop: "message_count",
}));
const __VLS_46 = __VLS_45({
    label: "互动",
    width: "100",
    sortable: true,
    prop: "message_count",
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
const { default: __VLS_49 } = __VLS_47.slots;
{
    const { default: __VLS_50 } = __VLS_47.slots;
    const [{ row }] = __VLS_vSlot(__VLS_50);
    (row.message_count);
    // @ts-ignore
    [];
}
// @ts-ignore
[];
var __VLS_47;
let __VLS_51;
/** @ts-ignore @type { | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column'] | typeof __VLS_components.elTableColumn | typeof __VLS_components.ElTableColumn | typeof __VLS_components['el-table-column']} */
elTableColumn;
// @ts-ignore
const __VLS_52 = __VLS_asFunctionalComponent1(__VLS_51, new __VLS_51({
    label: "最近互动",
    width: "130",
}));
const __VLS_53 = __VLS_52({
    label: "最近互动",
    width: "130",
}, ...__VLS_functionalComponentArgsRest(__VLS_52));
const { default: __VLS_56 } = __VLS_54.slots;
{
    const { default: __VLS_57 } = __VLS_54.slots;
    const [{ row }] = __VLS_vSlot(__VLS_57);
    (__VLS_ctx.relativeTime(row.last_interact_at));
    // @ts-ignore
    [relativeTime,];
}
// @ts-ignore
[];
var __VLS_54;
// @ts-ignore
[];
var __VLS_8;
// @ts-ignore
[];
const __VLS_export = (await import('vue')).defineComponent({});
export default {};
