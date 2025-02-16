from flask import Flask, render_template, jsonify, request
from flask_socketio import SocketIO
import json
import os

app = Flask(__name__)
socketio = SocketIO(app)

CONF_PATH = '/usr/Brutus/conf.json'
FIFO_PATH = '/usr/Brutus/test_pipe'
PORT = 5000

def read_config():
    with open(CONF_PATH, 'r') as file:
        return json.load(file)

def write_config(config, path, value):
    with open(CONF_PATH, 'w') as file:
        json.dump(config, file, indent=4)
    
    # Формируем сообщение в формате "/devices/{device}/controls/{control} value"
    path_parts = path.split('.')
    if len(path_parts) == 3:  # port/device/control
        port, device, control = path_parts
        fifo_message = f"/devices/{device}/controls/{control} {value}\n"
        
        try:
            if not os.path.exists(FIFO_PATH):
                os.mkfifo(FIFO_PATH)
            with open(FIFO_PATH, 'w') as fifo:
                fifo.write(fifo_message)
        except Exception as e:
            print(f"Error writing to FIFO: {e}")

@app.route('/')
def index():
    config = read_config()
    return render_template('index.html', initial_config=config)

@app.route('/get_config')
def get_config():
    return jsonify(read_config())

@app.route('/update', methods=['POST'])
def update_value():
    try:
        data = request.json
        path = data['path']
        value = data['value']
        
        # Преобразование значений
        if value.lower() == 'true':
            value = True
        elif value.lower() == 'false':
            value = False
        elif value.lower() == 'null':
            value = None
        else:
            try:
                value = float(value)
                if value.is_integer():
                    value = int(value)
            except ValueError:
                pass

        config = read_config()
        current = config
        path_parts = path.split('.')
        for part in path_parts[:-1]:
            current = current[part]
        current[path_parts[-1]] = value
        
        # Передаем path и value в функцию write_config
        write_config(config, path, str(value))
        socketio.emit('config_update', config)
        return jsonify({'success': True})
    except Exception as e:
        return jsonify({'success': False, 'error': str(e)})

if __name__ == '__main__':
    socketio.run(app, host='0.0.0.0', port=PORT, debug=True)