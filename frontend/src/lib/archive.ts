import { api, desktopCall, isDesktop } from "./api";
import type { Project } from "../domain/types";

interface Transfer {
  id: string;
  filename: string;
  size: number;
  chunk_bytes: number;
}
interface FileWriter {
  write: (data: Uint8Array) => Promise<void>;
  close: () => Promise<void>;
  abort: () => Promise<void>;
}
interface SaveHandle {
  createWritable: () => Promise<FileWriter>;
}
interface SavePicker {
  showSaveFilePicker?: (options: {
    suggestedName: string;
    types: { description: string; accept: Record<string, string[]> }[];
  }) => Promise<SaveHandle>;
}

/** Encode one bounded byte frame without exceeding the JavaScript argument-stack limit. */
function encodeChunk(bytes: Uint8Array) {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 32768)
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
  return btoa(binary);
}

/** Import either a historical or current JSON file through bounded, retryable frames. */
export async function restoreArchiveFile(file: File) {
  const transfer = await api<Transfer>("begin_archive_import", {
    size: file.size,
  });
  try {
    let offset = 0;
    while (offset < file.size) {
      const bytes = new Uint8Array(
        await file.slice(offset, offset + transfer.chunk_bytes).arrayBuffer(),
      );
      const frame = {
        transfer_id: transfer.id,
        offset,
        data: encodeChunk(bytes),
      };
      let result: { offset: number };
      try {
        result = await api("write_archive_chunk", frame);
      } catch {
        result = await api("write_archive_chunk", frame);
      }
      if (result.offset !== offset + bytes.length)
        throw new Error("The backup transfer returned an invalid offset");
      offset = result.offset;
    }
    /** Retry completion against the same durable transfer rather than uploading a second book. */
    const complete = () =>
      api<Project>("complete_archive_import", {
        transfer_id: transfer.id,
      });
    try {
      return await complete();
    } catch {
      // The same transfer retains its result if only the completion acknowledgement was lost.
      return await complete();
    }
  } finally {
    await api("finish_archive_transfer", { transfer_id: transfer.id }).catch(
      () => {},
    );
  }
}

/** Save a full backup through a native writer, browser stream or bounded compatibility download. */
export async function downloadArchive(
  projectId: string,
  title: string,
): Promise<boolean> {
  const filename = `${title}.quillweave.json`.replace(/[/\\:*?"<>|]/g, "_");
  const picker = window as unknown as SavePicker;
  let handle: SaveHandle | null = null;
  if (!isDesktop() && picker.showSaveFilePicker) {
    try {
      handle = await picker.showSaveFilePicker({
        suggestedName: filename,
        types: [
          {
            description: "Quillweave JSON backup",
            accept: { "application/json": [".json"] },
          },
        ],
      });
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError")
        return false;
      throw error;
    }
  }
  const transfer = await api<Transfer>("begin_archive_export", {
    project_id: projectId,
  });
  let writer: FileWriter | null = null;
  try {
    if (isDesktop())
      return await desktopCall<boolean>(
        "ExportArchiveTransfer",
        transfer.id,
        filename,
      );
    if (!handle && transfer.size > 128 * 1024 * 1024)
      throw new Error(
        "Use the desktop app or a browser with streaming file saving for this backup",
      );
    writer = handle ? await handle.createWritable() : null;
    const parts: BlobPart[] = [];
    let offset = 0;
    while (offset < transfer.size) {
      const frame = await api<{ data: string; offset: number; done: boolean }>(
        "read_archive_chunk",
        { transfer_id: transfer.id, offset },
      );
      const bytes = Uint8Array.from(atob(frame.data), (character) =>
        character.charCodeAt(0),
      );
      if (
        !bytes.length ||
        frame.offset !== offset + bytes.length ||
        frame.offset > transfer.size ||
        frame.done !== (frame.offset === transfer.size)
      )
        throw new Error("The backup transfer returned an invalid offset");
      if (writer) await writer.write(bytes);
      else parts.push(bytes);
      offset = frame.offset;
    }
    if (writer) {
      await writer.close();
      writer = null;
    } else {
      const url = URL.createObjectURL(
        new Blob(parts, { type: "application/json" }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    return true;
  } catch (error) {
    await writer?.abort().catch(() => {});
    throw error;
  } finally {
    await api("finish_archive_transfer", { transfer_id: transfer.id }).catch(
      () => {},
    );
  }
}
