/**
 * 需求图片的前端校验（后端 contract.RDCImageExts / MaxRDCImageBytes / MaxRDCImages 的镜像）。
 * 职责：提供白名单常量与 validateImageFiles，在 beforeUpload 与提交前各拦一道。
 * 输入：浏览器 File / RcFile 形状的对象（name / size / type）与已选数量。
 * 输出：合法返回空串，非法返回可直接上屏的中文提示。
 * 边界：只按文件名与 MIME 做粗筛，不读文件字节；真正的格式判定由 handler 嗅探、
 *       service 复校，三层约束常量必须一一对应。不上传、不发请求、不产生扩展名。
 */
import { RDC_IMAGE_ERRORS } from './format';

/** 允许的扩展名（镜像 contract.RDCImageExts 的键，jpeg 也接受）。 */
export const IMAGE_EXTS = ['png', 'jpg', 'jpeg', 'webp', 'gif'] as const;

/** 允许的 MIME（镜像 contract.RDCImageExts 的值集合）。 */
const IMAGE_MIMES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'] as const;

/** 单张上限 5MB、单需求最多 9 张（镜像 contract.MaxRDCImageBytes / MaxRDCImages）。 */
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
export const MAX_IMAGES = 9;

/** File 与 console-kit 的 RcFile 共有的最小形状。 */
export interface ImageCandidate {
  name: string;
  size: number;
  type: string;
}

/** 后缀白名单：文件名没有扩展名时直接判非法。 */
function hasAllowedExt(name: string): boolean {
  const dot = name.lastIndexOf('.');
  if (dot < 0) {
    return false;
  }
  const ext = name.slice(dot + 1).toLowerCase();
  return (IMAGE_EXTS as readonly string[]).includes(ext);
}

/**
 * 判断单个文件是否是可提交的图片。
 * 浏览器给出 MIME 时要求「MIME 与后缀同时命中」，避免把改了后缀的文本文件当图片；
 * MIME 为空（部分老浏览器 / 拖入文件）时退回只查后缀，最终仍由后端嗅探兜底。
 */
export function isImageFile(file: ImageCandidate): boolean {
  if (!hasAllowedExt(file.name)) {
    return false;
  }
  const type = file.type.toLowerCase();
  if (!type) {
    return true;
  }
  return (IMAGE_MIMES as readonly string[]).includes(type);
}

/**
 * 校验一批图片；existingCount 是此前已选的数量，用于「累计不超过 9 张」。
 * 返回空串表示通过，否则返回中文错误文案。
 */
export function validateImageFiles(files: readonly ImageCandidate[], existingCount = 0): string {
  if (existingCount + files.length > MAX_IMAGES) {
    return RDC_IMAGE_ERRORS.count;
  }
  for (const file of files) {
    if (!isImageFile(file)) {
      return RDC_IMAGE_ERRORS.format;
    }
    if (file.size <= 0 || file.size > MAX_IMAGE_BYTES) {
      return RDC_IMAGE_ERRORS.size;
    }
  }
  return '';
}
