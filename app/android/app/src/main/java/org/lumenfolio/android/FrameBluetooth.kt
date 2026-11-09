package org.lumenfolio.android

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.BluetoothStatusCodes
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanFilter
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.ParcelUuid
import android.os.SystemClock
import java.io.IOException
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.callbackFlow
import org.lumenfolio.mobilepanel.BluetoothConnection
import org.lumenfolio.mobilepanel.BluetoothDialer

private val serviceUuid = UUID.fromString("9fa16700-7e6b-4d75-89c4-5c79b25eac01")
private val writeUuid = UUID.fromString("9fa16701-7e6b-4d75-89c4-5c79b25eac01")
private val readUuid = UUID.fromString("9fa16702-7e6b-4d75-89c4-5c79b25eac01")
private const val defaultMtu = 23
private const val requestedMtu = 517

internal val frameBluetoothPermissions: Array<String>
    get() = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        arrayOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
    } else {
        arrayOf(Manifest.permission.ACCESS_FINE_LOCATION)
    }

internal data class NearbyFrame(val device: BluetoothDevice, val address: String, val name: String)

/** 仅接收广播相框服务 UUID 的设备，收集取消时归还系统扫描资源。 */
@SuppressLint("MissingPermission")
internal fun scanNearbyFrames(context: Context) = callbackFlow {
    val adapter = context.getSystemService(BluetoothManager::class.java)?.adapter
        ?: throw IOException("这台手机没有可用的蓝牙适配器")
    val scanner = adapter.bluetoothLeScanner ?: throw IOException("请先在系统设置中打开蓝牙")
    val callback = object : ScanCallback() {
        override fun onScanResult(callbackType: Int, result: ScanResult) {
            val address = result.device.address
            val name = result.scanRecord?.deviceName ?: "LumenFolio"
            trySend(NearbyFrame(result.device, address, name))
        }

        override fun onScanFailed(errorCode: Int) {
            close(IOException("蓝牙扫描失败（$errorCode）"))
        }
    }
    scanner.startScan(
        listOf(ScanFilter.Builder().setServiceUuid(ParcelUuid(serviceUuid)).build()),
        ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(),
        callback,
    )
    awaitClose { runCatching { scanner.stopScan(callback) } }
}

internal class FrameBluetoothDialer(
    private val context: Context,
    private val device: BluetoothDevice,
) : BluetoothDialer {
    override fun dialBluetooth(timeoutMillis: Long): BluetoothConnection =
        FrameGatt().apply { connect(context, device, timeoutMillis) }
}

/** Go 在后台串行调用读写。Android 的 GATT 回调在主线程，只完成各操作的回执。 */
@SuppressLint("MissingPermission")
@Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")
private class FrameGatt : BluetoothConnection {
    private val gatt = AtomicReference<BluetoothGatt?>()
    private val closed = AtomicBoolean()
    private val discovered = CompletableFuture<Unit>()
    private val mtuChanged = CompletableFuture<Int>()
    @Volatile private var operation: CompletableFuture<ByteArray>? = null
    private lateinit var writer: BluetoothGattCharacteristic
    private lateinit var reader: BluetoothGattCharacteristic
    private var maximumWrite = defaultMtu - 3

    private val callback = object : BluetoothGattCallback() {
        override fun onConnectionStateChange(client: BluetoothGatt, status: Int, newState: Int) {
            if (closed.get()) return
            if (status != BluetoothGatt.GATT_SUCCESS || newState == BluetoothProfile.STATE_DISCONNECTED) {
                close(IOException("相框蓝牙已断开（$status）"))
            } else if (newState == BluetoothProfile.STATE_CONNECTED && !client.discoverServices()) {
                close(IOException("无法读取相框蓝牙服务"))
            }
        }

        override fun onServicesDiscovered(client: BluetoothGatt, status: Int) {
            if (status == BluetoothGatt.GATT_SUCCESS) {
                discovered.complete(Unit)
            } else {
                discovered.completeExceptionally(IOException("无法读取相框蓝牙服务（$status）"))
            }
        }

        override fun onMtuChanged(client: BluetoothGatt, mtu: Int, status: Int) {
            mtuChanged.complete(if (status == BluetoothGatt.GATT_SUCCESS) mtu else defaultMtu)
        }

        override fun onCharacteristicWrite(
            client: BluetoothGatt, characteristic: BluetoothGattCharacteristic, status: Int,
        ) {
            if (characteristic.uuid == writeUuid) completeOperation(status, byteArrayOf())
        }

        override fun onCharacteristicRead(
            client: BluetoothGatt, characteristic: BluetoothGattCharacteristic, value: ByteArray, status: Int,
        ) {
            if (characteristic.uuid == readUuid) completeOperation(status, value)
        }

        override fun onCharacteristicRead(
            client: BluetoothGatt, characteristic: BluetoothGattCharacteristic, status: Int,
        ) {
            if (characteristic.uuid == readUuid) completeOperation(status, characteristic.value ?: byteArrayOf())
        }
    }

    fun connect(context: Context, device: BluetoothDevice, timeoutMillis: Long) {
        val deadline = SystemClock.elapsedRealtime() + timeoutMillis
        try {
            val client = device.connectGatt(
                context, false, callback, BluetoothDevice.TRANSPORT_LE,
                BluetoothDevice.PHY_LE_1M_MASK or BluetoothDevice.PHY_LE_2M_MASK,
                Handler(Looper.getMainLooper()),
            ) ?: throw IOException("无法连接相框蓝牙")
            gatt.set(client)
            if (closed.get()) gatt.getAndSet(null)?.close()
            discovered.awaitBluetooth(deadline - SystemClock.elapsedRealtime(), "连接相框蓝牙")
            val service = client.getService(serviceUuid) ?: throw IOException("相框缺少蓝牙传输服务")
            writer = service.getCharacteristic(writeUuid) ?: throw IOException("相框缺少蓝牙写入通道")
            reader = service.getCharacteristic(readUuid) ?: throw IOException("相框缺少蓝牙读取通道")
            client.requestConnectionPriority(BluetoothGatt.CONNECTION_PRIORITY_HIGH)
            val mtu = if (client.requestMtu(requestedMtu)) {
                try {
                    mtuChanged.get(minOf(2_000, (deadline - SystemClock.elapsedRealtime()).coerceAtLeast(1)), TimeUnit.MILLISECONDS)
                } catch (_: TimeoutException) {
                    defaultMtu
                }
            } else {
                defaultMtu
            }
            maximumWrite = (mtu - 3).coerceIn(defaultMtu - 3, 512)
        } catch (error: Exception) {
            closeBluetooth()
            throw error
        }
    }

    override fun maxWrite(): Int = maximumWrite

    override fun writeBluetooth(data: ByteArray, timeoutMillis: Long) {
        exchange(timeoutMillis, "写入相框蓝牙") { client ->
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                client.writeCharacteristic(writer, data, BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT) ==
                    BluetoothStatusCodes.SUCCESS
            } else {
                writer.writeType = BluetoothGattCharacteristic.WRITE_TYPE_DEFAULT
                writer.value = data
                client.writeCharacteristic(writer)
            }
        }
    }

    override fun readBluetooth(timeoutMillis: Long): ByteArray =
        exchange(timeoutMillis, "读取相框蓝牙") { it.readCharacteristic(reader) }

    private fun exchange(timeoutMillis: Long, action: String, start: (BluetoothGatt) -> Boolean): ByteArray {
        val client = gatt.get() ?: throw IOException("相框蓝牙已断开")
        val result = CompletableFuture<ByteArray>()
        operation = result
        var started = false
        try {
            started = !closed.get() && start(client)
            if (!started) throw IOException("无法${action}")
            return result.awaitBluetooth(timeoutMillis, action)
        } catch (error: Exception) {
            // 已收到的 ATT 错误只结束本次请求。仍有回调在途时，关闭连接以免它误完成下一次操作。
            if (started && !result.isDone) closeBluetooth()
            throw error
        } finally {
            operation = null
        }
    }

    private fun completeOperation(status: Int, value: ByteArray) {
        if (status == BluetoothGatt.GATT_SUCCESS) {
            operation?.complete(value)
        } else {
            operation?.completeExceptionally(IOException("相框蓝牙操作失败（$status）"))
        }
    }

    override fun closeBluetooth() = close(IOException("相框蓝牙已断开"))

    private fun close(error: IOException) {
        if (!closed.compareAndSet(false, true)) return
        discovered.completeExceptionally(error)
        mtuChanged.completeExceptionally(error)
        operation?.completeExceptionally(error)
        gatt.getAndSet(null)?.let { client ->
            runCatching { client.disconnect() }
            runCatching { client.close() }
        }
    }
}

private fun <T> CompletableFuture<T>.awaitBluetooth(timeoutMillis: Long, action: String): T {
    return try {
        get(timeoutMillis.coerceAtLeast(1), TimeUnit.MILLISECONDS)
    } catch (error: TimeoutException) {
        throw IOException("${action}超时", error)
    } catch (error: ExecutionException) {
        throw IOException(error.cause?.localizedMessage ?: "${action}失败", error.cause)
    }
}
