import axios from 'axios';
export const api = axios.create({
    baseURL: '/admin/api',
    timeout: 5000,
});
export async function getSnapshot(groupID, token) {
    const response = await api.get('/snapshot', {
        params: groupID ? { group_id: groupID } : undefined,
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
