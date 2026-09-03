import axios from 'axios';
export const api = axios.create({
    baseURL: '/admin/api',
    timeout: 5000,
});
export async function getSnapshot(groupID, token, windowMinutes = 1440) {
    const response = await api.get('/snapshot', {
        params: { ...(groupID ? { group_id: groupID } : {}), window_minutes: windowMinutes },
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    return response.data;
}
export async function getEventDetail(eventID, token) {
    const response = await api.get(`/events/${encodeURIComponent(eventID)}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    return response.data;
}
export async function getActivity(groupID, windowMinutes, type, page, token) {
    const response = await api.get('/activity', { params: { group_id: groupID || undefined, window_minutes: windowMinutes, type: type === 'all' ? undefined : type, page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined });
    return response.data;
}
export async function getTasks(status, token, page = 1) {
    const response = await api.get('/tasks', { params: { ...(status ? { status } : {}), page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined });
    return response.data;
}
export async function retryTask(taskID, token) {
    await api.post(`/tasks/${encodeURIComponent(taskID)}/retry`, undefined, { headers: token ? { Authorization: `Bearer ${token}` } : undefined });
}
export async function getMemories(groupID, status, type, query, page, token) {
    const response = await api.get('/memories', { params: { group_id: groupID || undefined, status, type: type || undefined, q: query.trim() || undefined, page }, headers: token ? { Authorization: `Bearer ${token}` } : undefined });
    return response.data;
}
export async function getMemes(groupID, query, token) {
    const response = await api.get('/memes', { params: { group_id: groupID || undefined, q: query.trim() || undefined }, headers: token ? { Authorization: `Bearer ${token}` } : undefined });
    return response.data;
}
export async function deleteMeme(memeID, token) {
    await api.delete(`/memes/${encodeURIComponent(memeID)}`, { headers: token ? { Authorization: `Bearer ${token}` } : undefined });
}
export async function getMCPConfig(token) {
    const response = await api.get('/mcp', {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    return response.data;
}
export async function updateMCPConfig(servers, token) {
    const response = await api.put('/mcp', { servers }, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        timeout: 25000,
    });
    return response.data;
}
